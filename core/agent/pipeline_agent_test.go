package agent

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestPipelineAgentGenerateReplyAddsAssistantMessageWithExtra(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{
					Content: "hello ",
					Extra:   map[string]any{"trace_id": "trace_1"},
				}},
				{Delta: &llm.ChoiceDelta{Content: "world"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items length = %d, want 1 assistant message", len(chatCtx.Items))
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("chatCtx item = %T, want *llm.ChatMessage", chatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "hello world" {
		t.Fatalf("assistant message = %#v, want assistant text hello world", msg)
	}
	if got := msg.Extra["trace_id"]; got != "trace_1" {
		t.Fatalf("assistant Extra[trace_id] = %#v, want trace_1", got)
	}
}

func TestPipelineAgentGenerateReplyWithInstructionsUsesTemporaryChatContext(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "user_1",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "brief answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		Instructions: "answer in one sentence",
	})

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	inferenceCtx := l.chatContexts[0]
	if len(inferenceCtx.Items) != 2 {
		t.Fatalf("inference chat item count = %d, want instruction and original user", len(inferenceCtx.Items))
	}
	instruction, ok := inferenceCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[0])
	}
	if instruction.Role != llm.ChatRoleSystem || instruction.TextContent() != "answer in one sentence" {
		t.Fatalf("instruction message = %#v, want temporary system instructions", instruction)
	}
	if len(chatCtx.Items) != 2 {
		t.Fatalf("persistent chat item count = %d, want original user and assistant only", len(chatCtx.Items))
	}
	if chatCtx.Items[0].GetID() != "user_1" {
		t.Fatalf("persistent first item ID = %q, want user_1", chatCtx.Items[0].GetID())
	}
	msg, ok := chatCtx.Items[1].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "brief answer" {
		t.Fatalf("persistent second item = %#v, want assistant response only", chatCtx.Items[1])
	}
}

func TestPipelineAgentGenerateReplyWithChatContextUsesTemporaryContext(t *testing.T) {
	persistentCtx := llm.NewChatContext()
	overrideCtx := llm.NewChatContext()
	overrideCtx.Append(&llm.ChatMessage{
		ID:      "override_user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "override"}},
	})
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "from override"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, persistentCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		ChatCtx: overrideCtx,
	})

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	if l.chatContexts[0] == overrideCtx {
		t.Fatal("LLM chat context aliases override context, want copy")
	}
	if len(l.chatContexts[0].Items) != 1 || l.chatContexts[0].Items[0].GetID() != "override_user" {
		t.Fatalf("LLM chat context items = %#v, want override context", l.chatContexts[0].Items)
	}
	if len(persistentCtx.Items) != 1 {
		t.Fatalf("persistent chat item count = %d, want assistant only", len(persistentCtx.Items))
	}
	msg, ok := persistentCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "from override" {
		t.Fatalf("persistent item = %#v, want assistant generated from override context", persistentCtx.Items[0])
	}
	if len(overrideCtx.Items) != 1 {
		t.Fatalf("override chat item count = %d, want unchanged", len(overrideCtx.Items))
	}
}

func TestPipelineAgentGenerateReplyWithChatContextCarriesToolOutputs(t *testing.T) {
	persistentCtx := llm.NewChatContext()
	overrideCtx := llm.NewChatContext()
	overrideCtx.Append(&llm.ChatMessage{
		ID:      "override_user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "override"}},
	})
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup",
							Arguments: `{}`,
						}},
					}},
				},
			},
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "done"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "tool result"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, persistentCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		ChatCtx: overrideCtx,
	})

	if len(l.chatContexts) != 2 {
		t.Fatalf("LLM chat contexts = %d, want initial and tool reply calls", len(l.chatContexts))
	}
	if l.chatContexts[1] == overrideCtx {
		t.Fatal("second LLM chat context aliases override context, want working copy")
	}
	if len(l.chatContexts[1].Items) != 3 {
		t.Fatalf("second LLM chat context items = %#v, want override user, function call, and function output", l.chatContexts[1].Items)
	}
	if l.chatContexts[1].Items[0].GetID() != "override_user" {
		t.Fatalf("second LLM first item = %#v, want override user", l.chatContexts[1].Items[0])
	}
	if _, ok := l.chatContexts[1].Items[1].(*llm.FunctionCall); !ok {
		t.Fatalf("second LLM second item = %T, want FunctionCall", l.chatContexts[1].Items[1])
	}
	output, ok := l.chatContexts[1].Items[2].(*llm.FunctionCallOutput)
	if !ok || output.CallID != "call_lookup" || output.Output != "tool result" {
		t.Fatalf("second LLM third item = %#v, want lookup tool result", l.chatContexts[1].Items[2])
	}
	if len(overrideCtx.Items) != 1 {
		t.Fatalf("override chat item count = %d, want unchanged", len(overrideCtx.Items))
	}
}

func TestPipelineAgentGenerateReplyWithToolChoicePassesChatOption(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "no tools"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		ToolChoice: "none",
	})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if l.calls[0].ToolChoice != "none" {
		t.Fatalf("ToolChoice = %#v, want none", l.calls[0].ToolChoice)
	}
}

func TestPipelineAgentGenerateReplyPassesSessionLLMChatOptions(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "configured"}},
			},
		},
	}
	parallel := true
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		LLMParallelToolCalls: &parallel,
		LLMExtraParams: map[string]any{
			"temperature": 0.2,
		},
		LLMResponseFormat: map[string]any{
			"type": "json_object",
		},
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	call := l.calls[0]
	if !call.ParallelToolCalls {
		t.Fatal("ParallelToolCalls = false, want true")
	}
	if call.ExtraParams["temperature"] != 0.2 {
		t.Fatalf("ExtraParams = %#v, want temperature", call.ExtraParams)
	}
	if call.ResponseFormat["type"] != "json_object" {
		t.Fatalf("ResponseFormat = %#v, want json_object", call.ResponseFormat)
	}
}

func TestPipelineAgentGenerateReplyWithToolsFiltersChatOptions(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "lookup only"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{
		&fakeGenerationTool{name: "lookup"},
		&fakeGenerationTool{name: "calendar"},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		Tools: []string{"lookup"},
	})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if len(l.calls[0].Tools) != 1 {
		t.Fatalf("LLM tools = %#v, want only lookup", l.calls[0].Tools)
	}
	if got := l.calls[0].Tools[0].ID(); got != "lookup" {
		t.Fatalf("LLM tool ID = %q, want lookup", got)
	}
}

func TestPipelineAgentGenerateReplyIncludesAgentToolsInChatOptions(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "with tools"}},
			},
		},
	}
	baseAgent := NewAgent("test")
	baseAgent.Tools = []llm.Tool{&fakeGenerationTool{name: "agent_tool"}}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "session_tool"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if got, want := generationToolNames(l.calls[0].Tools), []string{"agent_tool", "session_tool"}; !stringSlicesEqual(got, want) {
		t.Fatalf("LLM tools = %#v, want %#v", got, want)
	}
}

func TestPipelineAgentGenerateReplyWithToolsSelectsAgentTool(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "agent tool only"}},
			},
		},
	}
	baseAgent := NewAgent("test")
	baseAgent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup"}}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "calendar"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		Tools: []string{"lookup"},
	})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if got, want := generationToolNames(l.calls[0].Tools), []string{"lookup"}; !stringSlicesEqual(got, want) {
		t.Fatalf("LLM tools = %#v, want %#v", got, want)
	}
}

func TestPipelineAgentGenerateReplyWithToolsFiltersProviderTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "provider tool only"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	providerTool := &fakeProviderGenerationTool{fakeGenerationTool: fakeGenerationTool{name: "web_search"}}
	session.Tools = []llm.Tool{
		&fakeGenerationTool{name: "lookup"},
		providerTool,
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		Tools: []string{"web_search"},
	})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if len(l.calls[0].Tools) != 1 || l.calls[0].Tools[0] != providerTool {
		t.Fatalf("LLM tools = %#v, want only provider tool", l.calls[0].Tools)
	}
}

func TestPipelineAgentGenerateReplyWithToolsRejectsDuplicateFunctionNames(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "should not run"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{
		&fakeIDGenerationTool{id: "primary_lookup", name: "lookup"},
		&fakeIDGenerationTool{id: "backup_lookup", name: "lookup"},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		Tools: []string{"primary_lookup"},
	})

	if len(l.calls) != 0 {
		t.Fatalf("LLM Chat calls = %d, want no call when tools have duplicate function names", len(l.calls))
	}
	select {
	case ev := <-session.ErrorEvents():
		if ev.Error == nil || !strings.Contains(ev.Error.Error(), "duplicate function name: lookup") {
			t.Fatalf("Error = %v, want duplicate function name error", ev.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive duplicate function name error")
	}
}

func TestPipelineAgentEmitsConversationItemAddedForAssistantMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "hello"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("event item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "hello" {
			t.Fatalf("event message = %#v, want assistant hello", msg)
		}
		if len(chatCtx.Items) != 1 || chatCtx.Items[0] != msg {
			t.Fatalf("event item was not the committed chat item")
		}
		if ev.CreatedAt.IsZero() {
			t.Fatal("event CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive assistant message")
	}
}

func TestPipelineAgentEmitsSynchronizedAgentOutputTranscription(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "hello "}},
				{Delta: &llm.ChoiceDelta{Content: "world"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              make([]byte, 4000),
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 2000,
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	events := session.AgentOutputTranscribedEvents()

	agent.generateReply()

	select {
	case ev := <-events:
		if ev.Transcript == "" || !strings.Contains(ev.Transcript, "hello") {
			t.Fatalf("agent output transcript = %#v, want generated assistant text", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentOutputTranscribedEvents did not receive assistant transcript")
	}
}

func TestPipelineAgentUsesTTSAlignedTranscriptWhenEnabled(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "llm transcript"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		UseTTSAlignedTranscript: true,
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{
		capabilities: tts.TTSCapabilities{Streaming: true, AlignedTranscript: true},
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              make([]byte, 4000),
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 2000,
			}},
			timedTranscripts: [][]tts.TimedString{{
				{Text: "aligned transcript", StartTime: 0.1, EndTime: 0.5},
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	events := session.AgentOutputTranscribedEvents()

	agent.generateReply()

	select {
	case ev := <-events:
		if ev.Transcript != "aligned transcript" {
			t.Fatalf("agent output transcript = %q, want TTS aligned transcript", ev.Transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentOutputTranscribedEvents did not receive aligned assistant transcript")
	}
}

func TestPipelineAgentUsesAgentTTSAlignedTranscriptOverride(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "llm transcript"}},
			},
		},
	}
	baseAgent := NewAgent("test")
	baseAgent.UseTTSAlignedTranscript = true
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		UseTTSAlignedTranscript: false,
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{
		capabilities: tts.TTSCapabilities{Streaming: true, AlignedTranscript: true},
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              make([]byte, 4000),
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 2000,
			}},
			timedTranscripts: [][]tts.TimedString{{
				{Text: "agent aligned transcript", StartTime: 0.1, EndTime: 0.5},
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	events := session.AgentOutputTranscribedEvents()

	agent.generateReply()

	select {
	case ev := <-events:
		if ev.Transcript != "agent aligned transcript" {
			t.Fatalf("agent output transcript = %q, want agent override TTS aligned transcript", ev.Transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentOutputTranscribedEvents did not receive agent override aligned assistant transcript")
	}
}

func TestPipelineAgentAgentTTSAlignedTranscriptOverrideCanDisableSessionDefault(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "llm transcript"}},
			},
		},
	}
	baseAgent := NewAgent("test")
	baseAgent.UseTTSAlignedTranscript = false
	baseAgent.UseTTSAlignedTranscriptSet = true
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		UseTTSAlignedTranscript: true,
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{
		capabilities: tts.TTSCapabilities{Streaming: true, AlignedTranscript: true},
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              make([]byte, 4000),
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 2000,
			}},
			timedTranscripts: [][]tts.TimedString{{
				{Text: "aligned transcript", StartTime: 0.1, EndTime: 0.5},
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	events := session.AgentOutputTranscribedEvents()

	agent.generateReply()

	select {
	case ev := <-events:
		if ev.Transcript != "llm transcript" {
			t.Fatalf("agent output transcript = %q, want agent override to disable TTS aligned transcript", ev.Transcript)
		}
	case <-time.After(time.Second):
		t.Fatal("AgentOutputTranscribedEvents did not receive assistant transcript")
	}
}

func TestPipelineAgentEmitsConversationItemAddedForUserTranscript(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{},
	}, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	stream := &fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{{Text: "hello from user"}},
		}},
	}

	agent.sttLoop(stream)

	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("event item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello from user" {
			t.Fatalf("event message = %#v, want user transcript", msg)
		}
		if len(chatCtx.Items) == 0 || chatCtx.Items[0] != msg {
			t.Fatalf("event item was not the committed user chat item")
		}
		if len(session.ChatCtx.Items) != 1 || session.ChatCtx.Items[0] != msg {
			t.Fatalf("session ChatCtx items = %#v, want committed user message", session.ChatCtx.Items)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive user transcript")
	}
}

func TestPipelineAgentRoutesSTTTranscriptsThroughActivity(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	chatCtx := llm.NewChatContext()
	pipeline := NewPipelineAgent(nil, nil, baseAgent.LLM, &fakePipelineTTS{}, chatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline

	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{{
				Language:   "en",
				Text:       "hello through activity",
				SpeakerID:  "speaker_1",
				Confidence: 0.82,
			}},
		}},
	})

	transcriptEvent := receiveUserInputTranscribedEvent(t, session)
	if transcriptEvent.Transcript != "hello through activity" || !transcriptEvent.IsFinal {
		t.Fatalf("UserInputTranscribedEvent = %#v, want final transcript from activity", transcriptEvent)
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("ConversationItemAdded item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello through activity" {
			t.Fatalf("conversation message = %#v, want user transcript", msg)
		}
		if msg.TranscriptConfidence == nil || *msg.TranscriptConfidence != 0.82 {
			t.Fatalf("TranscriptConfidence = %v, want 0.82", msg.TranscriptConfidence)
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive activity-committed user transcript")
	}

	transcript, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{})
	if err != nil {
		t.Fatalf("CommitUserTurn error = %v, want nil", err)
	}
	if transcript != "" {
		t.Fatalf("CommitUserTurn transcript after pipeline activity commit = %q, want empty", transcript)
	}
}

func TestPipelineAgentEmitsUserInputTranscribedEvents(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{},
	}, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	stream := &fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{
			{
				Type: stt.SpeechEventInterimTranscript,
				Alternatives: []stt.SpeechData{{
					Language:  "en",
					Text:      "hello",
					SpeakerID: "speaker_1",
				}},
			},
			{
				Type: stt.SpeechEventFinalTranscript,
				Alternatives: []stt.SpeechData{{
					Language:  "en",
					Text:      "hello there",
					SpeakerID: "speaker_1",
				}},
			},
		},
	}

	agent.sttLoop(stream)

	events := []UserInputTranscribedEvent{
		receiveUserInputTranscribedEvent(t, session),
		receiveUserInputTranscribedEvent(t, session),
	}
	if events[0].Transcript != "hello" || events[0].IsFinal {
		t.Fatalf("interim transcript event = %#v, want non-final hello", events[0])
	}
	if events[1].Transcript != "hello there" || !events[1].IsFinal {
		t.Fatalf("final transcript event = %#v, want final hello there", events[1])
	}
	for i, ev := range events {
		if ev.Language != "en" || ev.SpeakerID != "speaker_1" {
			t.Fatalf("event %d language/speaker = %q/%q, want en/speaker_1", i, ev.Language, ev.SpeakerID)
		}
		if ev.CreatedAt.IsZero() {
			t.Fatalf("event %d CreatedAt is zero", i)
		}
	}
}

func TestPipelineAgentEmitsErrorEventForSTTStreamError(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	source := &fakePipelineSTT{}
	cause := errors.New("stt stream failed")
	agent := NewPipelineAgent(nil, source, &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{},
	}, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	stream := &fakePipelineRecognizeStream{err: cause}

	agent.sttLoop(stream)

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		var sttErr *stt.STTError
		if !errors.As(ev.Error, &sttErr) {
			t.Fatalf("Error = %T, want *stt.STTError", ev.Error)
		}
		if sttErr.Label != "fake-stt" || sttErr.Recoverable {
			t.Fatalf("STTError = %#v, want fake-stt unrecoverable error", sttErr)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want STT source", ev.Source)
		}
		if ev.CreatedAt.IsZero() {
			t.Fatal("CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive STT stream error")
	}
}

func TestPipelineAgentEmitsErrorEventForVADStreamError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	source := &fakePipelineVAD{}
	cause := errors.New("vad stream failed")
	agent := NewPipelineAgent(source, nil, nil, nil, nil)
	agent.session = session

	agent.vadLoop(&fakePipelineVADStream{err: cause})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want VAD source", ev.Source)
		}
		if ev.CreatedAt.IsZero() {
			t.Fatal("CreatedAt is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive VAD stream error")
	}
}

func TestPipelineAgentVADLoopForwardsSpeechEventsToActivity(t *testing.T) {
	endpointing := &recordingPipelineEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	pipeline := NewPipelineAgent(&fakePipelineVAD{}, nil, nil, nil, nil)
	pipeline.session = session

	pipeline.vadLoop(&fakePipelineVADStream{
		events: []*vad.VADEvent{
			{Type: vad.VADEventStartOfSpeech, Timestamp: 1.25},
			{Type: vad.VADEventEndOfSpeech, Timestamp: 2.5},
		},
	})

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() = %q, want listening after VAD end", got)
	}
	if endpointing.startCount != 1 || endpointing.startAt != 1.25 {
		t.Fatalf("endpointing start = (%d, %.2f), want (1, 1.25)", endpointing.startCount, endpointing.startAt)
	}
	if endpointing.endCount != 1 || endpointing.endAt != 2.5 {
		t.Fatalf("endpointing end = (%d, %.2f), want (1, 2.50)", endpointing.endCount, endpointing.endAt)
	}
}

func TestPipelineAgentVADLoopForwardsInferenceEventsToActivity(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		TurnDetection:           TurnDetectionModeVAD,
		MinInterruptionDuration: 0.05,
	})
	activity := NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	session.activity = activity
	pipeline := NewPipelineAgent(&fakePipelineVAD{}, nil, nil, nil, nil)
	pipeline.session = session

	pipeline.vadLoop(&fakePipelineVADStream{
		events: []*vad.VADEvent{
			{
				Type:                  vad.VADEventInferenceDone,
				SpeechDuration:        0.06,
				Speaking:              true,
				RawAccumulatedSilence: 0,
			},
		},
	})

	waitForInterrupted(t, current)
	current.MarkDone()
}

func TestPipelineAgentEmitsLLMErrorEventForChatFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("llm chat failed")
	l := &failingPipelineLLM{
		label: "test.LLM",
		err:   cause,
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	select {
	case ev := <-session.ErrorEvents():
		llmErr, ok := ev.Error.(*llm.LLMError)
		if !ok {
			t.Fatalf("Error = %T, want *llm.LLMError", ev.Error)
		}
		if !errors.Is(llmErr, cause) {
			t.Fatalf("LLMError unwrap = %v, want %v", llmErr, cause)
		}
		if llmErr.Label != "test.LLM" || llmErr.Recoverable {
			t.Fatalf("LLMError = %#v, want label test.LLM recoverable false", llmErr)
		}
		if ev.Source != l {
			t.Fatalf("Source = %#v, want LLM source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive LLM error")
	}
}

func TestPipelineAgentEmitsLLMMetricsForUsageChunk(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	l := &fakeGenerationLLM{
		model:    "test-model",
		provider: "test-provider",
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{
					ID: "chatcmpl_123",
					Delta: &llm.ChoiceDelta{
						Content: "hello",
					},
				},
				{
					ID: "chatcmpl_123",
					Usage: &llm.CompletionUsage{
						PromptTokens:       7,
						PromptCachedTokens: 2,
						CompletionTokens:   5,
						TotalTokens:        12,
					},
				},
			},
		},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	select {
	case ev := <-session.MetricsCollectedEvents():
		metrics, ok := ev.Metrics.(*telemetry.LLMMetrics)
		if !ok {
			t.Fatalf("Metrics = %T, want *telemetry.LLMMetrics", ev.Metrics)
		}
		if metrics.Label != "agent.fakeGenerationLLM" {
			t.Fatalf("Label = %q, want agent.fakeGenerationLLM", metrics.Label)
		}
		if metrics.RequestID != "chatcmpl_123" {
			t.Fatalf("RequestID = %q, want chatcmpl_123", metrics.RequestID)
		}
		if metrics.PromptTokens != 7 || metrics.PromptCachedTokens != 2 || metrics.CompletionTokens != 5 || metrics.TotalTokens != 12 {
			t.Fatalf("token metrics = %#v, want prompt=7 cached=2 completion=5 total=12", metrics)
		}
		if metrics.Metadata == nil || metrics.Metadata.ModelName != "test-model" || metrics.Metadata.ModelProvider != "test-provider" {
			t.Fatalf("Metadata = %#v, want test provider/model", metrics.Metadata)
		}
		if metrics.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive LLM metrics")
	}
}

func TestPipelineAgentReturnsToThinkingWhileExecutingTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{
					Content: "checking",
					ToolCalls: []llm.FunctionToolCall{{
						Type:      "function",
						Name:      "lookup",
						CallID:    "call_lookup",
						Arguments: `{}`,
					}},
				}},
			},
		},
	}
	tool := &blockingPipelineTool{
		name:    "lookup",
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{tool}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	if got := currentAgentState(session); got != AgentStateInitializing {
		t.Fatalf("initial AgentState = %q, want %q", got, AgentStateInitializing)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.generateReply()
	}()

	select {
	case <-tool.started:
	case <-time.After(time.Second):
		t.Fatal("tool execution did not start")
	}
	if got := currentAgentState(session); got != AgentStateThinking {
		t.Fatalf("AgentState while tool is running = %q, want %q", got, AgentStateThinking)
	}

	close(tool.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generateReply did not finish after releasing tool")
	}
}

func TestPipelineAgentForcesNoToolsAfterMaxToolSteps(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						Content: "checking",
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup",
							Arguments: `{}`,
						}},
					}},
				},
			},
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						Content: "final answer",
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup_after_none",
							Arguments: `{}`,
						}},
					}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{MaxToolSteps: 1})
	tool := &countingPipelineTool{name: "lookup", result: "done"}
	session.Tools = []llm.Tool{tool}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	if len(l.calls) != 2 {
		t.Fatalf("LLM Chat calls = %d, want 2", len(l.calls))
	}
	if l.calls[0].ToolChoice != nil {
		t.Fatalf("first ToolChoice = %#v, want nil", l.calls[0].ToolChoice)
	}
	if l.calls[1].ToolChoice != "none" {
		t.Fatalf("second ToolChoice = %#v, want none", l.calls[1].ToolChoice)
	}
	if tool.calls != 1 {
		t.Fatalf("tool executions = %d, want only first tool call before tool_choice none", tool.calls)
	}
}

func TestPipelineAgentDefaultsMaxToolStepsToThree(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			toolCallStream("call_lookup_1"),
			toolCallStream("call_lookup_2"),
			toolCallStream("call_lookup_3"),
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "final answer"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "done"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	if len(l.calls) != 4 {
		t.Fatalf("LLM Chat calls = %d, want 4", len(l.calls))
	}
	for i := 0; i < 3; i++ {
		if l.calls[i].ToolChoice != nil {
			t.Fatalf("call %d ToolChoice = %#v, want nil", i+1, l.calls[i].ToolChoice)
		}
	}
	if l.calls[3].ToolChoice != "none" {
		t.Fatalf("fourth ToolChoice = %#v, want none after default max tool steps", l.calls[3].ToolChoice)
	}
}

func TestPipelineAgentEmitsFunctionToolsExecutedEvent(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup",
							Arguments: `{}`,
						}},
					}},
				},
			},
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "done"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "tool result"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if ev.GetType() != "function_tools_executed" {
			t.Fatalf("event type = %q, want function_tools_executed", ev.GetType())
		}
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want call_lookup", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0].Output != "tool result" {
			t.Fatalf("FunctionCallOutputs = %#v, want tool result", ev.FunctionCallOutputs)
		}
		if !ev.HasToolReply() {
			t.Fatal("HasToolReply() = false, want true when tool returned output")
		}
		if len(session.ChatCtx.Items) != 3 {
			t.Fatalf("session ChatCtx items = %#v, want function call, output, and follow-up assistant", session.ChatCtx.Items)
		}
		if session.ChatCtx.Items[0] != ev.FunctionCalls[0] {
			t.Fatalf("session ChatCtx first item = %#v, want emitted function call", session.ChatCtx.Items[0])
		}
		if session.ChatCtx.Items[1] != ev.FunctionCallOutputs[0] {
			t.Fatalf("session ChatCtx second item = %#v, want emitted function output", session.ChatCtx.Items[1])
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive event")
	}
}

func TestPipelineAgentScheduledReplyProvidesSpeechHandleToTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup",
							Arguments: `{}`,
						}},
					}},
				},
			},
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "done"}},
				},
			},
		},
	}
	tool := &runContextRecordingTool{}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{tool}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.GenerateReply(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline reply did not complete: %v", err)
	}
	if tool.runContext == nil {
		t.Fatal("tool run context is nil")
	}
	if tool.runContext.SpeechHandle != handle {
		t.Fatalf("tool run context SpeechHandle = %#v, want scheduled handle %#v", tool.runContext.SpeechHandle, handle)
	}
	if len(handle.ChatItems()) != 1 {
		t.Fatalf("handle chat items = %#v, want generated assistant message", handle.ChatItems())
	}
}

func TestPipelineAgentScheduledReplyIncrementsSpeechStepForToolReply(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup",
							Arguments: `{}`,
						}},
					}},
				},
			},
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "done"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "tool result"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.GenerateReply(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline reply did not complete: %v", err)
	}
	if got, want := handle.NumSteps(), 2; got != want {
		t.Fatalf("handle.NumSteps() = %d, want %d after tool follow-up reply", got, want)
	}
}

func TestPipelineAgentScheduledReplyIncludesUserMessageInInferenceContext(t *testing.T) {
	pipelineCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, pipelineCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.GenerateReply(context.Background(), "reply to this")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline reply did not complete: %v", err)
	}
	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	inferenceCtx := l.chatContexts[0]
	if len(inferenceCtx.Items) == 0 {
		t.Fatal("inference chat context is empty, want scheduled user message")
	}
	for _, item := range inferenceCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		if msg.Role == llm.ChatRoleUser && msg.TextContent() == "reply to this" {
			return
		}
	}
	t.Fatalf("inference chat context items = %#v, want scheduled user input", inferenceCtx.Items)
}

func TestPipelineAgentScheduledReplyPersistsUserMessageInAgentChatContext(t *testing.T) {
	pipelineCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, pipelineCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.GenerateReply(context.Background(), "remember this")
	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline reply did not complete: %v", err)
	}
	var userMessages int
	for _, item := range pipelineCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		if msg.Role == llm.ChatRoleUser && msg.TextContent() == "remember this" {
			userMessages++
		}
	}
	if userMessages != 1 {
		t.Fatalf("pipeline chat context items = %#v, want one scheduled user message", pipelineCtx.Items)
	}
}

func TestPipelineAgentScheduledSaySpeaksProvidedTextWithoutLLM(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "wrong"}},
			},
		},
	}
	ttsStream := &fakePipelineTTSStream{}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{stream: ttsStream}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.Say(context.Background(), "speak this text")
	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline say did not complete: %v", err)
	}
	if got := ttsStream.text.String(); got != "speak this text" {
		t.Fatalf("TTS text = %q, want scheduled say text", got)
	}
	if len(l.calls) != 0 {
		t.Fatalf("LLM calls = %d, want 0 for scheduled say", len(l.calls))
	}
	items := handle.ChatItems()
	if len(items) != 1 {
		t.Fatalf("handle chat items = %#v, want committed say assistant message", items)
	}
	msg, ok := items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("handle chat item = %T, want *llm.ChatMessage", items[0])
	}
	if msg.Role != llm.ChatRoleAssistant || msg.TextContent() != "speak this text" {
		t.Fatalf("handle chat message = %#v, want assistant say text", msg)
	}
}

func TestPipelineAgentScheduledSayPersistsAssistantTextInAgentChatContext(t *testing.T) {
	pipelineCtx := llm.NewChatContext()
	ttsStream := &fakePipelineTTSStream{}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{}, &fakePipelineTTS{stream: ttsStream}, pipelineCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()

	handle, err := session.Say(context.Background(), "persist this")
	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled pipeline say did not complete: %v", err)
	}
	var assistantMessages int
	for _, item := range pipelineCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		if msg.Role == llm.ChatRoleAssistant && msg.TextContent() == "persist this" {
			assistantMessages++
		}
	}
	if assistantMessages != 1 {
		t.Fatalf("pipeline chat context items = %#v, want one scheduled say assistant message", pipelineCtx.Items)
	}
}

func toolCallStream(callID string) *fakeGenerationLLMStream {
	return &fakeGenerationLLMStream{
		chunks: []*llm.ChatChunk{
			{Delta: &llm.ChoiceDelta{
				ToolCalls: []llm.FunctionToolCall{{
					Type:      "function",
					Name:      "lookup",
					CallID:    callID,
					Arguments: `{}`,
				}},
			}},
		},
	}
}

type fakePipelineTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter
	stream       *fakePipelineTTSStream
	capabilities tts.TTSCapabilities
}

func (f *fakePipelineTTS) Label() string { return "fake" }

func (f *fakePipelineTTS) Capabilities() tts.TTSCapabilities {
	if f.capabilities != (tts.TTSCapabilities{}) {
		return f.capabilities
	}
	return tts.TTSCapabilities{Streaming: true}
}

func (f *fakePipelineTTS) SampleRate() int { return 24000 }

func (f *fakePipelineTTS) NumChannels() int { return 1 }

func (f *fakePipelineTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}

func (f *fakePipelineTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	if f.stream == nil {
		f.stream = &fakePipelineTTSStream{}
	}
	return f.stream, nil
}

type failingPipelineLLM struct {
	label string
	err   error
}

func (f *failingPipelineLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return nil, f.err
}

func (f *failingPipelineLLM) Label() string { return f.label }

type fakeProviderGenerationTool struct {
	fakeGenerationTool
}

func (f *fakeProviderGenerationTool) IsProviderTool() bool { return true }

type fakeIDGenerationTool struct {
	id   string
	name string
}

func (f *fakeIDGenerationTool) ID() string { return f.id }

func (f *fakeIDGenerationTool) Name() string { return f.name }

func (f *fakeIDGenerationTool) Description() string { return "" }

func (f *fakeIDGenerationTool) Parameters() map[string]any { return nil }

func (f *fakeIDGenerationTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type fakePipelineTTSStream struct {
	text             strings.Builder
	frames           []*model.AudioFrame
	timedTranscripts [][]tts.TimedString
	next             int
}

func (f *fakePipelineTTSStream) PushText(text string) error {
	_, _ = f.text.WriteString(text)
	return nil
}

func (f *fakePipelineTTSStream) Flush() error { return nil }

func (f *fakePipelineTTSStream) Close() error { return nil }

func (f *fakePipelineTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if f.next < len(f.frames) {
		frame := f.frames[f.next]
		var timedTranscript []tts.TimedString
		if f.next < len(f.timedTranscripts) {
			timedTranscript = f.timedTranscripts[f.next]
		}
		f.next++
		return &tts.SynthesizedAudio{Frame: frame, TimedTranscript: timedTranscript}, nil
	}
	return nil, io.EOF
}

type fakePipelineSTT struct{}

func (f *fakePipelineSTT) Label() string { return "fake-stt" }

func (f *fakePipelineSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}

func (f *fakePipelineSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	return &fakePipelineRecognizeStream{}, nil
}

func (f *fakePipelineSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type fakePipelineVAD struct {
	metricsHandlers []vad.VADMetricsHandler
}

func (f *fakePipelineVAD) Label() string { return "fake-vad" }

func (f *fakePipelineVAD) Model() string { return "fake-vad" }

func (f *fakePipelineVAD) Provider() string { return "fake" }

func (f *fakePipelineVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{}
}

func (f *fakePipelineVAD) OnMetricsCollected(handler vad.VADMetricsHandler) {
	if handler != nil {
		f.metricsHandlers = append(f.metricsHandlers, handler)
	}
}

func (f *fakePipelineVAD) EmitMetricsCollected(metrics *telemetry.VADMetrics) {
	for _, handler := range f.metricsHandlers {
		handler(metrics)
	}
}

func (f *fakePipelineVAD) Stream(context.Context) (vad.VADStream, error) {
	return &fakePipelineVADStream{}, nil
}

type fakePipelineVADStream struct {
	events []*vad.VADEvent
	index  int
	err    error
}

func (f *fakePipelineVADStream) PushFrame(*model.AudioFrame) error { return nil }

func (f *fakePipelineVADStream) Flush() error { return nil }

func (f *fakePipelineVADStream) EndInput() error { return nil }

func (f *fakePipelineVADStream) Close() error { return nil }

func (f *fakePipelineVADStream) Next() (*vad.VADEvent, error) {
	if f.index < len(f.events) {
		ev := f.events[f.index]
		f.index++
		return ev, nil
	}
	if f.err != nil {
		err := f.err
		f.err = nil
		return nil, err
	}
	return nil, io.EOF
}

type recordingPipelineEndpointing struct {
	startCount int
	endCount   int
	startAt    float64
	endAt      float64
}

func (r *recordingPipelineEndpointing) UpdateOptions(*float64, *float64) {}
func (r *recordingPipelineEndpointing) MinDelay() float64                { return 0 }
func (r *recordingPipelineEndpointing) MaxDelay() float64                { return 0 }
func (r *recordingPipelineEndpointing) Overlapping() bool                { return false }
func (r *recordingPipelineEndpointing) OnStartOfAgentSpeech(float64)     {}
func (r *recordingPipelineEndpointing) OnEndOfAgentSpeech(float64)       {}

func (r *recordingPipelineEndpointing) OnStartOfSpeech(startedAt float64, overlapping bool) {
	r.startCount++
	r.startAt = startedAt
}

func (r *recordingPipelineEndpointing) OnEndOfSpeech(endedAt float64, shouldIgnore bool) {
	r.endCount++
	r.endAt = endedAt
}

type blockingPipelineTool struct {
	name    string
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type countingPipelineTool struct {
	name   string
	result string
	calls  int
}

func (c *countingPipelineTool) ID() string { return c.name }

func (c *countingPipelineTool) Name() string { return c.name }

func (c *countingPipelineTool) Description() string { return "" }

func (c *countingPipelineTool) Parameters() map[string]any { return nil }

func (c *countingPipelineTool) Execute(context.Context, string) (string, error) {
	c.calls++
	return c.result, nil
}

func (b *blockingPipelineTool) ID() string { return b.name }

func (b *blockingPipelineTool) Name() string { return b.name }

func (b *blockingPipelineTool) Description() string { return "" }

func (b *blockingPipelineTool) Parameters() map[string]any { return nil }

func (b *blockingPipelineTool) Execute(ctx context.Context, _ string) (string, error) {
	b.once.Do(func() { close(b.started) })
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-b.release:
		return "done", nil
	}
}

func currentAgentState(session *AgentSession) AgentState {
	return session.AgentState()
}

func generationToolNames(tools []llm.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func receiveUserInputTranscribedEvent(t *testing.T, session *AgentSession) UserInputTranscribedEvent {
	t.Helper()

	select {
	case ev := <-session.UserInputTranscribedEvents():
		return ev
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive transcript")
	}
	return UserInputTranscribedEvent{}
}

type fakePipelineRecognizeStream struct {
	events []*stt.SpeechEvent
	index  int
	err    error
}

func (f *fakePipelineRecognizeStream) PushFrame(*model.AudioFrame) error { return nil }

func (f *fakePipelineRecognizeStream) Flush() error { return nil }

func (f *fakePipelineRecognizeStream) Close() error { return nil }

func (f *fakePipelineRecognizeStream) Next() (*stt.SpeechEvent, error) {
	if f.index >= len(f.events) {
		if f.err != nil {
			err := f.err
			f.err = nil
			return nil, err
		}
		return nil, io.EOF
	}
	ev := f.events[f.index]
	f.index++
	return ev, nil
}
