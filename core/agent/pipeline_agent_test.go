package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
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

func TestSessionRegisteredToolsAddsCancellationHelpersForCancellableTools(t *testing.T) {
	agent := NewAgent("test")
	agent.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", flags: llm.ToolFlagCancellable}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	tools, err := sessionRegisteredTools(context.Background(), session)
	if err != nil {
		t.Fatalf("sessionRegisteredTools() error = %v, want nil", err)
	}
	got := sortedAgentToolNames(agentToolsAsInterfaces(tools))
	want := []string{"lk_agents_cancel_task", "lk_agents_get_running_tasks", "lookup"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sessionRegisteredTools() names = %#v, want reference cancellation helpers %#v", got, want)
	}
}

func TestPipelineAgentGenerateReplyAddsAssistantMessageTTSMetrics(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		model:    "test-llm",
		provider: "test-llm-provider",
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "hello"}},
			},
		},
	}
	ttsStream := &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{Data: []byte{1, 2}}},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{
		model:    "test-voice",
		provider: "test-tts-provider",
		stream:   ttsStream,
	}, chatCtx)
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
	if got, ok := msg.Metrics["llm_node_ttft"].(float64); !ok || got <= 0 {
		t.Fatalf("assistant Metrics[llm_node_ttft] = %#v, want positive first token latency", msg.Metrics["llm_node_ttft"])
	}
	if got, ok := msg.Metrics["tts_node_ttfb"].(float64); !ok || got <= 0 {
		t.Fatalf("assistant Metrics[tts_node_ttfb] = %#v, want positive first audio latency", msg.Metrics["tts_node_ttfb"])
	}
	assertAssistantMetricMetadata(t, msg.Metrics, "llm_metadata", "test-llm", "test-llm-provider")
	assertAssistantMetricMetadata(t, msg.Metrics, "tts_metadata", "test-voice", "test-tts-provider")
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
		t.Fatalf("inference chat item count = %d, want original user and appended instruction", len(inferenceCtx.Items))
	}
	first, ok := inferenceCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[0])
	}
	if first.GetID() != "user_1" || first.Role != llm.ChatRoleUser || first.TextContent() != "hello" {
		t.Fatalf("first inference item = %#v, want original user before temporary instructions", first)
	}
	instruction, ok := inferenceCtx.Items[1].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("second inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[1])
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

func TestPipelineAgentPrecomputeReplyAppendsInstructionsLikeReference(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:      "user_1",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "precomputed answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)

	_, err := agent.precomputeLLMGeneration(context.Background(), session, pipelineReplyOptions{
		Instructions: "answer in one sentence",
	})
	if err != nil {
		t.Fatalf("precomputeLLMGeneration error = %v", err)
	}

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	inferenceCtx := l.chatContexts[0]
	if len(inferenceCtx.Items) != 2 {
		t.Fatalf("inference chat item count = %d, want original user and appended instruction", len(inferenceCtx.Items))
	}
	first, ok := inferenceCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[0])
	}
	if first.GetID() != "user_1" || first.Role != llm.ChatRoleUser || first.TextContent() != "hello" {
		t.Fatalf("first inference item = %#v, want original user before temporary instructions", first)
	}
	instruction, ok := inferenceCtx.Items[1].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("second inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[1])
	}
	if instruction.Role != llm.ChatRoleSystem || instruction.TextContent() != "answer in one sentence" {
		t.Fatalf("instruction message = %#v, want appended temporary system instructions", instruction)
	}
	if len(chatCtx.Items) != 1 || chatCtx.Items[0].GetID() != "user_1" {
		t.Fatalf("persistent chat items = %#v, want only original user", chatCtx.Items)
	}
}

func TestPipelineAgentGenerateReplyAppliesInstructionInputModality(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "audio answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, InputDetails{Modality: "audio"})
	speech.Generation.Instructions = llm.NewInstructions("speak plainly", "write tersely")

	agent.OnSpeechScheduled(context.Background(), speech)

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	inferenceCtx := l.chatContexts[0]
	if len(inferenceCtx.Items) == 0 {
		t.Fatal("inference chat context is empty, want instruction message")
	}
	instruction, ok := inferenceCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[0])
	}
	if instruction.Role != llm.ChatRoleSystem || instruction.TextContent() != "speak plainly" {
		t.Fatalf("instruction message = %#v, want audio-specific instructions", instruction)
	}
}

func TestPipelineAgentGenerateReplyAppliesAgentInstructionInputModality(t *testing.T) {
	baseAgent := NewAgent("")
	baseAgent.InstructionVariants = llm.NewInstructions("speak plainly", "write tersely")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	activity.Start()
	defer activity.Stop()

	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "audio answer"}},
			},
		},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, baseAgent.ChatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, InputDetails{Modality: "audio"})

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want 1", len(l.chatContexts))
	}
	inferenceCtx := l.chatContexts[0]
	if len(inferenceCtx.Items) == 0 {
		t.Fatal("inference chat context is empty, want agent instruction message")
	}
	instruction, ok := inferenceCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first inference item = %T, want *llm.ChatMessage", inferenceCtx.Items[0])
	}
	if instruction.Role != llm.ChatRoleSystem || instruction.TextContent() != "speak plainly" {
		t.Fatalf("instruction message = %#v, want audio-specific agent instructions", instruction)
	}
	persistent, ok := baseAgent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok || persistent.TextContent() != "speak plainly" {
		t.Fatalf("persistent instruction message = %#v, want original audio representation unchanged", baseAgent.ChatCtx.Items[0])
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

func TestPipelineAgentToolReplyRefreshesUpdatedInstructions(t *testing.T) {
	baseAgent := NewAgent("old instructions")
	if err := updateAgentInstructionsMessage(baseAgent.ChatCtx, llm.NewInstructions(baseAgent.Instructions), true); err != nil {
		t.Fatalf("updateAgentInstructionsMessage error = %v", err)
	}
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "update_instructions",
							CallID:    "call_update_instructions",
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
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	baseAgent.activity = activity
	session.activity = activity
	session.Tools = []llm.Tool{updateInstructionsPipelineTool{instructions: "new instructions"}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, baseAgent.ChatCtx)
	agent.session = session
	agent.ctx = context.Background()
	replyCtx := baseAgent.ChatCtx.Copy()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		ChatCtx: replyCtx,
	})

	if len(l.chatContexts) != 2 {
		t.Fatalf("LLM chat contexts = %d, want initial and tool reply calls", len(l.chatContexts))
	}
	instructions := instructionMessageFromContext(t, l.chatContexts[1])
	if got := instructions.TextContent(); got != "new instructions" {
		t.Fatalf("tool reply instructions = %q, want updated instructions", got)
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

func TestPipelineAgentGenerateReplyOnEnterFiltersIgnoredTools(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "normal tool only"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{
		&fakeGenerationTool{name: "lookup"},
		&fakeGenerationTool{name: "workflow_step", flags: llm.ToolFlagIgnoreOnEnter},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{
		IgnoreOnEnterTools: true,
	})

	if len(l.calls) != 1 {
		t.Fatalf("LLM Chat calls = %d, want 1", len(l.calls))
	}
	if got, want := generationToolNames(l.calls[0].Tools), []string{"lookup"}; !stringSlicesEqual(got, want) {
		t.Fatalf("LLM tools = %#v, want %#v", got, want)
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
	if len(l.calls[0].Tools) != 2 {
		t.Fatalf("LLM tools = %d, want session and agent tools", len(l.calls[0].Tools))
	}
	if got, want := generationToolNames(l.calls[0].Tools), []string{"session_tool", "agent_tool"}; !stringSlicesEqual(got, want) {
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

func TestFormatPythonStringListUsesReferenceStringRepr(t *testing.T) {
	got := formatPythonStringList([]string{"lookup", "can't", "\u2028"})
	want := `['lookup', "can't", '\u2028']`
	if got != want {
		t.Fatalf("formatPythonStringList() = %q, want %q", got, want)
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

func TestPipelineAgentEmitsFinalSynchronizedAgentOutputTranscription(t *testing.T) {
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

	var final AgentOutputTranscribedEvent
	for deadline := time.After(time.Second); !final.IsFinal; {
		select {
		case ev := <-events:
			final = ev
		case <-deadline:
			t.Fatal("AgentOutputTranscribedEvents did not receive final assistant transcript")
		}
	}
	if final.Transcript != "hello world" {
		t.Fatalf("final transcript = %q, want full assistant text", final.Transcript)
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

func TestPipelineAgentEmitsFinalAlignedAgentOutputTranscription(t *testing.T) {
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
				{Text: "Halo, ", StartTime: 0.0, EndTime: 0.2},
				{Text: "ada yang bisa saya bantu?", StartTime: 0.2, EndTime: 1.0},
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	events := session.AgentOutputTranscribedEvents()

	agent.generateReply()

	var final AgentOutputTranscribedEvent
	for deadline := time.After(time.Second); !final.IsFinal; {
		select {
		case ev := <-events:
			final = ev
		case <-deadline:
			t.Fatal("AgentOutputTranscribedEvents did not receive final aligned assistant transcript")
		}
	}
	if final.Transcript != "Halo, ada yang bisa saya bantu?" {
		t.Fatalf("final transcript = %q, want full aligned assistant text", final.Transcript)
	}
}

func TestPipelineAgentMarksSpeakingAfterFirstAudioFrame(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, nil, nil, llm.NewChatContext())
	ttsGen := &TTSGenerationData{
		AudioCh:     make(chan *model.AudioFrame),
		TimedTextCh: make(chan tts.TimedString),
	}
	transcriptSync := NewTranscriptSynchronizer(0)
	defer transcriptSync.Close()
	done := closedChannel()

	playDone := make(chan error, 1)
	go func() {
		_, err := agent.playTTSGenerationWithTranscript(context.Background(), session, ttsGen, transcriptSync, done, nil)
		playDone <- err
	}()

	select {
	case ev := <-session.AgentStateChangedCh:
		t.Fatalf("agent state changed before first audio frame: %#v", ev)
	case <-time.After(50 * time.Millisecond):
	}

	ttsGen.AudioCh <- &model.AudioFrame{
		Data:              []byte{1, 2},
		SampleRate:        1000,
		NumChannels:       1,
		SamplesPerChannel: 100,
	}
	close(ttsGen.AudioCh)
	close(ttsGen.TimedTextCh)

	select {
	case ev := <-session.AgentStateChangedCh:
		if ev.NewState != AgentStateSpeaking {
			t.Fatalf("agent state event = %#v, want speaking after first audio frame", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not enter speaking after first audio frame")
	}
	select {
	case err := <-playDone:
		if err != nil {
			t.Fatalf("playTTSGenerationWithTranscript error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("playTTSGenerationWithTranscript did not finish")
	}
}

func TestPipelineAgentSayReturnsDirectSpeechToListeningWithoutIdle(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "direct speech"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "direct speech"}},
	}
	activity.currentSpeech = speech
	speech.AddDoneCallback(activity.OnPipelineReplyDone)
	agent := NewPipelineAgent(nil, nil, nil, &fakePipelineTTS{
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              []byte{1, 2},
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 100,
			}},
		},
	}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()

	agent.OnSpeechScheduled(context.Background(), speech)

	var got []AgentState
	for {
		select {
		case ev := <-session.AgentStateChangedCh:
			got = append(got, ev.NewState)
		default:
			want := []AgentState{AgentStateSpeaking, AgentStateListening}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("agent states = %#v, want %#v without transient idle", got, want)
			}
			return
		}
	}
}

func TestPipelineAgentGenerateReplyReturnsToListeningWithoutIdle(t *testing.T) {
	baseAgent := NewAgent("test")
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	speech := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = speech
	speech.AddDoneCallback(activity.OnPipelineReplyDone)
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: "reply"}}},
		},
	}, &fakePipelineTTS{
		stream: &fakePipelineTTSStream{
			frames: []*model.AudioFrame{{
				Data:              []byte{1, 2},
				SampleRate:        1000,
				NumChannels:       1,
				SamplesPerChannel: 100,
			}},
		},
	}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.OnSpeechScheduled(context.Background(), speech)

	var got []AgentState
	for {
		select {
		case ev := <-session.AgentStateChangedCh:
			got = append(got, ev.NewState)
		default:
			want := []AgentState{AgentStateThinking, AgentStateSpeaking, AgentStateListening}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("agent states = %#v, want %#v without transient idle", got, want)
			}
			return
		}
	}
}

func TestPipelineAgentGenerateReplyErrorReturnsToListeningWithoutIdle(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	speech := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = speech
	speech.AddDoneCallback(activity.OnPipelineReplyDone)
	agent := NewPipelineAgent(nil, nil, erroringPipelineLLM{err: errors.New("llm failed")}, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()

	agent.OnSpeechScheduled(context.Background(), speech)

	var got []AgentState
	for {
		select {
		case ev := <-session.AgentStateChangedCh:
			got = append(got, ev.NewState)
		default:
			want := []AgentState{AgentStateThinking, AgentStateListening}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("agent states = %#v, want %#v without transient idle after LLM error", got, want)
			}
			return
		}
	}
}

func TestPipelineAgentSendsSilenceToSTTDuringAECWarmup(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{AECWarmupDuration: 0.05})
	session.UpdateAgentState(AgentStateSpeaking)
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: vadStream},
		&fakePipelineSTT{stream: sttStream},
		nil,
		nil,
		llm.NewChatContext(),
	)
	agent.session = session

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	agent.OnAudioFrame(ctx, frame)

	vadFrame := receivePipelineFrame(t, vadStream.pushedCh)
	if !bytes.Equal(vadFrame.Data, frame.Data) {
		t.Fatalf("VAD frame data = %v, want original %v", vadFrame.Data, frame.Data)
	}

	sttFrame := receivePipelineFrame(t, sttStream.pushedCh)
	if sttFrame == frame {
		t.Fatal("STT frame reused original frame during AEC warmup")
	}
	if sttFrame.SampleRate != frame.SampleRate || sttFrame.NumChannels != frame.NumChannels || sttFrame.SamplesPerChannel != frame.SamplesPerChannel {
		t.Fatalf("STT silence shape = rate %d channels %d samples %d, want rate %d channels %d samples %d",
			sttFrame.SampleRate, sttFrame.NumChannels, sttFrame.SamplesPerChannel,
			frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel)
	}
	if !bytes.Equal(sttFrame.Data, make([]byte, len(frame.Data))) {
		t.Fatalf("STT frame data = %v, want silence", sttFrame.Data)
	}
}

func TestPipelineAgentSendsSilenceToSTTDuringUninterruptibleSpeech(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{DiscardAudioIfUninterruptible: true})
	activity := NewAgentActivity(NewAgent("test"), session)
	activity.currentSpeech = NewSpeechHandle(false, DefaultInputDetails())
	session.activity = activity
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: vadStream},
		&fakePipelineSTT{stream: sttStream},
		nil,
		nil,
		llm.NewChatContext(),
	)
	agent.session = session

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	frame := &model.AudioFrame{
		Data:              []byte{5, 6, 7, 8},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	agent.OnAudioFrame(ctx, frame)

	vadFrame := receivePipelineFrame(t, vadStream.pushedCh)
	if !bytes.Equal(vadFrame.Data, frame.Data) {
		t.Fatalf("VAD frame data = %v, want original %v", vadFrame.Data, frame.Data)
	}

	sttFrame := receivePipelineFrame(t, sttStream.pushedCh)
	if sttFrame == frame {
		t.Fatal("STT frame reused original frame during uninterruptible speech")
	}
	if !bytes.Equal(sttFrame.Data, make([]byte, len(frame.Data))) {
		t.Fatalf("STT frame data = %v, want silence", sttFrame.Data)
	}
}

func TestPipelineAgentAudioInputHookTransformsFramesBeforeVADAndSTT(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: vadStream},
		&fakePipelineSTT{stream: sttStream},
		nil,
		nil,
		llm.NewChatContext(),
	)
	agent.SetAudioInputHook(AudioInputHook(func(_ context.Context, frame *model.AudioFrame) *model.AudioFrame {
		data := append([]byte(nil), frame.Data...)
		data[0] = 9
		return &model.AudioFrame{
			Data:              data,
			SampleRate:        frame.SampleRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: frame.SamplesPerChannel,
		}
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	frame := &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}
	agent.OnAudioFrame(ctx, frame)

	vadFrame := receivePipelineFrame(t, vadStream.pushedCh)
	sttFrame := receivePipelineFrame(t, sttStream.pushedCh)

	if bytes.Equal(vadFrame.Data, frame.Data) || bytes.Equal(sttFrame.Data, frame.Data) {
		t.Fatalf("hook did not transform frames: VAD %v STT %v original %v", vadFrame.Data, sttFrame.Data, frame.Data)
	}
	if !bytes.Equal(vadFrame.Data, []byte{9, 2, 3, 4}) {
		t.Fatalf("VAD frame data = %v, want transformed audio", vadFrame.Data)
	}
	if !bytes.Equal(sttFrame.Data, []byte{9, 2, 3, 4}) {
		t.Fatalf("STT frame data = %v, want transformed audio", sttFrame.Data)
	}
	if !bytes.Equal(frame.Data, []byte{1, 2, 3, 4}) {
		t.Fatalf("original frame mutated to %v", frame.Data)
	}
}

func TestPipelineAgentAudioInputHookCanDropFrames(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: vadStream},
		&fakePipelineSTT{stream: sttStream},
		nil,
		nil,
		llm.NewChatContext(),
	)
	agent.SetAudioInputHook(AudioInputHook(func(context.Context, *model.AudioFrame) *model.AudioFrame {
		return nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	select {
	case frame := <-vadStream.pushedCh:
		t.Fatalf("VAD received frame %#v, want dropped", frame)
	case frame := <-sttStream.pushedCh:
		t.Fatalf("STT received frame %#v, want dropped", frame)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAgentSessionAudioInputHookConfiguresPipelineAssistant(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{stream: vadStream}
	baseAgent.STT = &fakePipelineSTT{stream: sttStream}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		AudioInputHook: func(_ context.Context, frame *model.AudioFrame) *model.AudioFrame {
			changed := append([]byte(nil), frame.Data...)
			changed[0] = 7
			return &model.AudioFrame{
				Data:              changed,
				SampleRate:        frame.SampleRate,
				NumChannels:       frame.NumChannels,
				SamplesPerChannel: frame.SamplesPerChannel,
			}
		},
	})

	agent, ok := session.EnsureAssistant().(*PipelineAgent)
	if !ok {
		t.Fatalf("assistant = %T, want *PipelineAgent", session.Assistant)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	vadFrame := receivePipelineFrame(t, vadStream.pushedCh)
	sttFrame := receivePipelineFrame(t, sttStream.pushedCh)
	if !bytes.Equal(vadFrame.Data, []byte{7, 2, 3, 4}) {
		t.Fatalf("VAD frame data = %v, want session hook transformed audio", vadFrame.Data)
	}
	if !bytes.Equal(sttFrame.Data, []byte{7, 2, 3, 4}) {
		t.Fatalf("STT frame data = %v, want session hook transformed audio", sttFrame.Data)
	}
}

func TestPipelineAgentResamplesToSTTInputSampleRate(t *testing.T) {
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	stt := &fakePipelineSTTWithSampleRate{
		fakePipelineSTT: fakePipelineSTT{stream: sttStream},
		inputSampleRate: 8000,
	}
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}},
		stt,
		nil,
		nil,
		llm.NewChatContext(),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{
		Data:              make([]byte, 640),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 320,
	})

	pushed := receivePipelineFrame(t, sttStream.pushedCh)
	if pushed.SampleRate != 8000 {
		t.Fatalf("STT frame sample rate = %d, want 8000 (resampled to STT input rate)", pushed.SampleRate)
	}
}

func TestAgentSessionAudioInputHookConfiguresReplacementPipelineAssistant(t *testing.T) {
	vadStream := &fakePipelineVADStream{pushedCh: make(chan *model.AudioFrame, 1)}
	sttStream := &fakePipelineRecognizeStream{pushedCh: make(chan *model.AudioFrame, 1)}
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{stream: vadStream}
	baseAgent.STT = &fakePipelineSTT{stream: sttStream}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		AudioInputHook: func(_ context.Context, frame *model.AudioFrame) *model.AudioFrame {
			changed := append([]byte(nil), frame.Data...)
			changed[0] = 8
			return &model.AudioFrame{
				Data:              changed,
				SampleRate:        frame.SampleRate,
				NumChannels:       frame.NumChannels,
				SamplesPerChannel: frame.SamplesPerChannel,
			}
		},
	})

	replacement, ok := session.replacementAssistantLocked(&MultimodalAgent{}).(*PipelineAgent)
	if !ok {
		t.Fatalf("replacement assistant = %T, want *PipelineAgent", replacement)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go replacement.run(ctx)

	replacement.OnAudioFrame(ctx, &model.AudioFrame{
		Data:              []byte{1, 2, 3, 4},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	vadFrame := receivePipelineFrame(t, vadStream.pushedCh)
	sttFrame := receivePipelineFrame(t, sttStream.pushedCh)
	if !bytes.Equal(vadFrame.Data, []byte{8, 2, 3, 4}) {
		t.Fatalf("VAD frame data = %v, want replacement hook transformed audio", vadFrame.Data)
	}
	if !bytes.Equal(sttFrame.Data, []byte{8, 2, 3, 4}) {
		t.Fatalf("STT frame data = %v, want replacement hook transformed audio", sttFrame.Data)
	}
}

func TestPipelineAgentSessionOptionDisablesTTSTextTransforms(t *testing.T) {
	providerStream := &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte("audio"),
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
	}
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		DisableTTSTextTransforms: true,
	})
	agent := NewPipelineAgent(nil, nil, nil, &fakePipelineTTS{stream: providerStream}, llm.NewChatContext())

	if _, err := agent.synthesizeSpeech(context.Background(), session, singleTextChannel("Say **bold** now"), nil); err != nil {
		t.Fatalf("synthesizeSpeech error = %v", err)
	}

	if got, want := providerStream.text.String(), "Say **bold** now"; got != want {
		t.Fatalf("pushed TTS text = %q, want raw text %q", got, want)
	}
}

func TestPipelineAgentSessionOptionSelectsTTSTextTransforms(t *testing.T) {
	providerStream := &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte("audio"),
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
	}
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{
		TTSTextTransforms:    []string{"filter_emoji"},
		TTSTextTransformsSet: true,
	})
	agent := NewPipelineAgent(nil, nil, nil, &fakePipelineTTS{stream: providerStream}, llm.NewChatContext())

	if _, err := agent.synthesizeSpeech(context.Background(), session, singleTextChannel("Say **hi** 😊"), nil); err != nil {
		t.Fatalf("synthesizeSpeech error = %v", err)
	}

	if got, want := providerStream.text.String(), "Say **hi** "; got != want {
		t.Fatalf("pushed TTS text = %q, want selected transform output %q", got, want)
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
	userTranscriptEvents := session.UserInputTranscribedEvents()
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

	transcriptEvent := receiveUserInputTranscribedEvent(t, userTranscriptEvents)
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

func TestPipelineAgentRoutesPreflightTranscriptAsInterim(t *testing.T) {
	baseAgent := NewAgent("test")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	userTranscriptEvents := session.UserInputTranscribedEvents()
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	pipeline := NewPipelineAgent(nil, nil, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline

	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{{
			Type: stt.SpeechEventPreflightTranscript,
			Alternatives: []stt.SpeechData{{
				Language:   "en",
				Text:       "preflight user text",
				SpeakerID:  "speaker_1",
				Confidence: 0.75,
			}},
		}},
	})

	transcriptEvent := receiveUserInputTranscribedEvent(t, userTranscriptEvents)
	if transcriptEvent.Transcript != "preflight user text" || transcriptEvent.IsFinal {
		t.Fatalf("UserInputTranscribedEvent = %#v, want non-final preflight transcript", transcriptEvent)
	}
	if activity.pendingInterimTranscript != "preflight user text" {
		t.Fatalf("pending interim transcript = %q, want preflight user text", activity.pendingInterimTranscript)
	}
}

func TestPipelineAgentVADTurnDetectionWaitsForEndOfSpeechBeforeCommit(t *testing.T) {
	baseAgent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	baseAgent.TurnDetection = TurnDetectionModeVAD
	baseAgent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(baseAgent, session)
	baseAgent.activity = activity
	session.activity = activity
	defer activity.Stop()

	pipeline := NewPipelineAgent(baseAgent.VAD, &fakePipelineSTT{}, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline

	activity.OnStartOfSpeech(&vad.VADEvent{Timestamp: 1.0})
	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{{
			Type:         stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{{Text: "finish after vad", Confidence: 0.9}},
		}},
	})

	select {
	case msg := <-baseAgent.turns:
		t.Fatalf("OnUserTurnCompleted called before VAD end-of-speech with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.5})

	select {
	case msg := <-baseAgent.turns:
		if msg.TextContent() != "finish after vad" {
			t.Fatalf("turn message text = %q, want finish after vad", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after VAD end-of-speech")
	}
}

func TestPipelineAgentVADTurnDetectionIgnoresSTTEndOfSpeech(t *testing.T) {
	baseAgent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	baseAgent.TurnDetection = TurnDetectionModeVAD
	baseAgent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(baseAgent, session)
	baseAgent.activity = activity
	session.activity = activity
	defer activity.Stop()

	pipeline := NewPipelineAgent(baseAgent.VAD, &fakePipelineSTT{}, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline

	activity.OnStartOfSpeech(&vad.VADEvent{Timestamp: 1.0})
	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{
			{
				Type:         stt.SpeechEventFinalTranscript,
				Alternatives: []stt.SpeechData{{Text: "wait for vad only", Confidence: 0.9}},
			},
			{Type: stt.SpeechEventEndOfSpeech},
		},
	})

	select {
	case msg := <-baseAgent.turns:
		t.Fatalf("OnUserTurnCompleted called from STT end-of-speech in VAD mode with %q", msg.TextContent())
	case <-time.After(20 * time.Millisecond):
	}

	activity.OnEndOfSpeech(&vad.VADEvent{Timestamp: 1.5})

	select {
	case msg := <-baseAgent.turns:
		if msg.TextContent() != "wait for vad only" {
			t.Fatalf("turn message text = %q, want wait for vad only", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after VAD end-of-speech")
	}
}

func TestPipelineAgentSTTTurnDetectionWaitsForEndOfSpeechBeforeCommit(t *testing.T) {
	baseAgent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	baseAgent.TurnDetection = TurnDetectionModeSTT
	baseAgent.STT = &fakePipelineSTT{}
	endpointing := &recordingPipelineEndpointing{}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{Endpointing: endpointing, MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(baseAgent, session)
	baseAgent.activity = activity
	session.activity = activity
	defer activity.Stop()

	pipeline := NewPipelineAgent(nil, baseAgent.STT, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline

	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{
			{Type: stt.SpeechEventStartOfSpeech},
			{
				Type:         stt.SpeechEventFinalTranscript,
				Alternatives: []stt.SpeechData{{Text: "finish after stt eos", Confidence: 0.9}},
			},
			{Type: stt.SpeechEventEndOfSpeech},
		},
	})

	if endpointing.startCount != 1 {
		t.Fatalf("OnStartOfSpeech calls = %d, want 1 for STT start_of_speech", endpointing.startCount)
	}
	if endpointing.endCount != 1 {
		t.Fatalf("OnEndOfSpeech calls = %d, want 1 for STT end_of_speech", endpointing.endCount)
	}
	select {
	case msg := <-baseAgent.turns:
		if msg.TextContent() != "finish after stt eos" {
			t.Fatalf("turn message text = %q, want finish after stt eos", msg.TextContent())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("OnUserTurnCompleted was not called after STT end-of-speech")
	}
}

func TestPipelineAgentSTTEndOfSpeechFlushesActiveVADSegment(t *testing.T) {
	baseAgent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
	baseAgent.TurnDetection = TurnDetectionModeSTT
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01})
	activity := NewAgentActivity(baseAgent, session)
	baseAgent.activity = activity
	session.activity = activity
	defer activity.Stop()

	vadStream := &fakePipelineVADStream{}
	pipeline := NewPipelineAgent(baseAgent.VAD, baseAgent.STT, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	pipeline.ctx = context.Background()
	pipeline.vadStream = vadStream
	pipeline.vadSpeechStarted = true

	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{
			{Type: stt.SpeechEventStartOfSpeech},
			{
				Type:         stt.SpeechEventFinalTranscript,
				Alternatives: []stt.SpeechData{{Text: "stt eos flushes vad", Confidence: 0.9}},
			},
			{Type: stt.SpeechEventEndOfSpeech},
		},
	})

	if vadStream.flushCount != 1 {
		t.Fatalf("VAD Flush calls = %d, want 1 after STT end-of-speech during active VAD segment", vadStream.flushCount)
	}
}

func TestPipelineAgentSTTStartSpeechUsesProviderSpeechStartTime(t *testing.T) {
	endpointing := &recordingPipelineEndpointing{}
	baseAgent := NewAgent("test")
	baseAgent.TurnDetection = TurnDetectionModeSTT
	baseAgent.STT = &fakePipelineSTT{}
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(baseAgent, session)
	session.activity = activity
	defer activity.Stop()
	pipeline := NewPipelineAgent(nil, baseAgent.STT, nil, nil, baseAgent.ChatCtx)
	pipeline.session = session
	startTime := 42.5

	pipeline.sttLoop(&fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{{
			Type:            stt.SpeechEventStartOfSpeech,
			SpeechStartTime: &startTime,
		}},
	})

	if endpointing.startCount != 1 {
		t.Fatalf("OnStartOfSpeech calls = %d, want 1", endpointing.startCount)
	}
	if endpointing.startAt != startTime {
		t.Fatalf("endpointing startAt = %v, want provider speech_start_time %v", endpointing.startAt, startTime)
	}
	if got := timeToUnixSeconds(activity.userSpeechStartedAt); got != startTime {
		t.Fatalf("userSpeechStartedAt = %v, want provider speech_start_time %v", got, startTime)
	}
}

func TestPipelineAgentFlushInputTranscriptionPushesSilenceAndFlushesSTT(t *testing.T) {
	stream := &fakePipelineRecognizeStream{}
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	pipeline.sttStream = stream
	pipeline.lastSTTFrame = &model.AudioFrame{
		SampleRate:        1000,
		NumChannels:       1,
		SamplesPerChannel: 20,
	}

	if err := pipeline.FlushInputTranscription(context.Background(), 450*time.Millisecond); err != nil {
		t.Fatalf("FlushInputTranscription error = %v, want nil", err)
	}
	if stream.flushCount != 1 {
		t.Fatalf("Flush calls = %d, want 1", stream.flushCount)
	}
	if len(stream.frames) != 3 {
		t.Fatalf("silence frames = %d, want 3", len(stream.frames))
	}
	wantSamples := []uint32{200, 200, 50}
	for i, frame := range stream.frames {
		if frame.SampleRate != 1000 || frame.NumChannels != 1 || frame.SamplesPerChannel != wantSamples[i] {
			t.Fatalf("silence frame %d shape = rate %d channels %d samples %d, want 1000/1/%d",
				i, frame.SampleRate, frame.NumChannels, frame.SamplesPerChannel, wantSamples[i])
		}
		for _, sample := range frame.Data {
			if sample != 0 {
				t.Fatalf("silence frame %d contains non-zero data byte %d", i, sample)
			}
		}
	}
}

func TestPipelineAgentClearInputTranscriptionReplacesSTTStream(t *testing.T) {
	first := &fakePipelineRecognizeStream{closedCh: make(chan struct{})}
	second := &fakePipelineRecognizeStream{}
	sttObj := &queuedPipelineSTT{streams: []stt.RecognizeStream{second}}
	pipeline := NewPipelineAgent(nil, sttObj, nil, nil, nil)
	pipeline.sttStream = first
	pipeline.ctx = context.Background()

	if err := pipeline.ClearInputTranscription(); err != nil {
		t.Fatalf("ClearInputTranscription error = %v, want nil", err)
	}

	select {
	case <-first.closedCh:
	case <-time.After(time.Second):
		t.Fatal("old STT stream was not closed")
	}
	if pipeline.sttStream != second {
		t.Fatalf("pipeline sttStream = %#v, want replacement stream", pipeline.sttStream)
	}

	frame := &model.AudioFrame{
		Data:              []byte{0x01, 0x00},
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}
	pipeline.pushSTTFrame(frame)

	if len(first.frames) != 0 {
		t.Fatalf("old stream frames = %d, want 0 after clear", len(first.frames))
	}
	if len(second.frames) != 1 {
		t.Fatalf("new stream frames = %d, want 1 after clear", len(second.frames))
	}
}

func TestPipelineAgentEmitsUserInputTranscribedEvents(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	userTranscriptEvents := session.UserInputTranscribedEvents()
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
		receiveUserInputTranscribedEvent(t, userTranscriptEvents),
		receiveUserInputTranscribedEvent(t, userTranscriptEvents),
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

func TestPipelineAgentIgnoresCanceledSTTStreamOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	agent := NewPipelineAgent(nil, &fakePipelineSTT{}, nil, nil, llm.NewChatContext())
	agent.session = session

	agent.sttLoop(&fakePipelineRecognizeStream{err: context.Canceled})

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for stream cancellation", ev)
	default:
	}
}

func TestPipelineAgentEmitsErrorEventForSTTPushFrameError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("stt push failed")
	source := &fakePipelineSTT{stream: &fakePipelineRecognizeStream{pushErr: cause}}
	agent := NewPipelineAgent(&fakePipelineVAD{}, source, nil, nil, llm.NewChatContext())
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})

	select {
	case ev := <-session.ErrorEvents():
		var sttErr *stt.STTError
		if !errors.As(ev.Error, &sttErr) {
			t.Fatalf("Error = %T, want *stt.STTError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want STT source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive STT push error")
	}
}

func TestPipelineAgentIgnoresCanceledSTTPushFrameOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	pushed := make(chan *model.AudioFrame, 1)
	source := &fakePipelineSTT{
		stream: &fakePipelineRecognizeStream{
			pushErr:  context.Canceled,
			pushedCh: pushed,
		},
	}
	agent := NewPipelineAgent(&fakePipelineVAD{}, source, nil, nil, llm.NewChatContext())
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, pushed)

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for canceled STT push", ev)
	default:
	}
}

func TestPipelineAgentIgnoresClosedPipeSTTPushFrameOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	pushed := make(chan *model.AudioFrame, 1)
	source := &fakePipelineSTT{
		stream: &fakePipelineRecognizeStream{
			pushErr:  io.ErrClosedPipe,
			pushedCh: pushed,
		},
	}
	agent := NewPipelineAgent(&fakePipelineVAD{}, source, nil, nil, llm.NewChatContext())
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, pushed)

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for closed STT stream push", ev)
	default:
	}
}

func TestPipelineAgentClosesRecognitionStreamsOnContextCancel(t *testing.T) {
	vadClosed := make(chan struct{})
	sttClosed := make(chan struct{})
	vadPushed := make(chan *model.AudioFrame, 1)
	sttPushed := make(chan *model.AudioFrame, 1)
	vadStream := &fakePipelineVADStream{pushedCh: vadPushed, closedCh: vadClosed}
	sttStream := &fakePipelineRecognizeStream{pushedCh: sttPushed, closedCh: sttClosed}
	agent := NewPipelineAgent(
		&fakePipelineVAD{stream: vadStream},
		&fakePipelineSTT{stream: sttStream},
		nil,
		nil,
		llm.NewChatContext(),
	)
	ctx, cancel := context.WithCancel(context.Background())
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, vadPushed)
	receivePipelineFrame(t, sttPushed)
	cancel()

	receivePipelineClosed(t, vadClosed, "VAD")
	receivePipelineClosed(t, sttClosed, "STT")
}

func TestPipelineAgentStartsWithoutVAD(t *testing.T) {
	sttClosed := make(chan struct{})
	sttPushed := make(chan *model.AudioFrame, 1)
	sttStream := &fakePipelineRecognizeStream{pushedCh: sttPushed, closedCh: sttClosed}
	agent := NewPipelineAgent(nil, &fakePipelineSTT{stream: sttStream}, nil, nil, llm.NewChatContext())
	ctx, cancel := context.WithCancel(context.Background())
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, sttPushed)
	cancel()
	receivePipelineClosed(t, sttClosed, "STT")
}

func TestPipelineAgentStartsWithoutSTT(t *testing.T) {
	vadClosed := make(chan struct{})
	vadPushed := make(chan *model.AudioFrame, 1)
	vadStream := &fakePipelineVADStream{pushedCh: vadPushed, closedCh: vadClosed}
	agent := NewPipelineAgent(&fakePipelineVAD{stream: vadStream}, nil, nil, nil, llm.NewChatContext())
	ctx, cancel := context.WithCancel(context.Background())
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, vadPushed)
	cancel()
	receivePipelineClosed(t, vadClosed, "VAD")
}

func TestPipelineAgentEmitsErrorEventForSTTStreamStartError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("stt start failed")
	source := &fakePipelineSTT{streamErr: cause}
	agent := NewPipelineAgent(&fakePipelineVAD{}, source, nil, nil, llm.NewChatContext())
	agent.session = session

	agent.run(context.Background())

	select {
	case ev := <-session.ErrorEvents():
		var sttErr *stt.STTError
		if !errors.As(ev.Error, &sttErr) {
			t.Fatalf("Error = %T, want *stt.STTError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != source {
			t.Fatalf("Source = %#v, want STT source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive STT start error")
	}
}

func TestPipelineAgentIgnoresCanceledSTTStreamStartOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	agent := NewPipelineAgent(&fakePipelineVAD{}, &fakePipelineSTT{streamErr: context.Canceled}, nil, nil, llm.NewChatContext())
	agent.session = session

	agent.run(context.Background())

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for canceled STT stream start", ev)
	default:
	}
}

func TestPipelineAgentEmitsSTTMetricsForRecognitionUsage(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	source := &fakePipelineSTT{}
	agent := NewPipelineAgent(nil, source, nil, nil, llm.NewChatContext())
	agent.session = session
	stream := &fakePipelineRecognizeStream{
		events: []*stt.SpeechEvent{
			{
				Type:      stt.SpeechEventRecognitionUsage,
				RequestID: "stt_req_123",
				RecognitionUsage: &stt.RecognitionUsage{
					AudioDuration: 1.25,
					InputTokens:   3,
					OutputTokens:  5,
				},
			},
		},
	}

	agent.sttLoop(stream)

	select {
	case ev := <-session.MetricsCollectedEvents():
		metrics, ok := ev.Metrics.(*telemetry.STTMetrics)
		if !ok {
			t.Fatalf("Metrics = %T, want *telemetry.STTMetrics", ev.Metrics)
		}
		if metrics.Label != "fake-stt" {
			t.Fatalf("Label = %q, want fake-stt", metrics.Label)
		}
		if metrics.RequestID != "stt_req_123" {
			t.Fatalf("RequestID = %q, want stt_req_123", metrics.RequestID)
		}
		if metrics.AudioDuration != 1.25 || metrics.InputTokens != 3 || metrics.OutputTokens != 5 {
			t.Fatalf("usage metrics = %#v, want audio=1.25 input=3 output=5", metrics)
		}
		if !metrics.Streamed {
			t.Fatal("Streamed = false, want true")
		}
		if metrics.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive STT metrics")
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

func TestPipelineAgentIgnoresCanceledVADStreamStartOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	agent := NewPipelineAgent(&fakePipelineVAD{streamErr: context.Canceled}, &fakePipelineSTT{}, nil, nil, nil)
	agent.session = session

	agent.run(context.Background())

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for canceled VAD stream start", ev)
	default:
	}
}

func TestPipelineAgentIgnoresCanceledVADStreamOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	agent := NewPipelineAgent(&fakePipelineVAD{}, nil, nil, nil, nil)
	agent.session = session

	agent.vadLoop(&fakePipelineVADStream{err: context.Canceled})

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for stream cancellation", ev)
	default:
	}
}

func TestPipelineAgentIgnoresCanceledVADPushFrameOnShutdown(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	errorEvents := session.ErrorEvents()
	pushed := make(chan *model.AudioFrame, 1)
	source := &fakePipelineVAD{
		stream: &fakePipelineVADStream{
			pushErr:  context.Canceled,
			pushedCh: pushed,
		},
	}
	agent := NewPipelineAgent(source, &fakePipelineSTT{}, nil, nil, nil)
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.run(ctx)

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte{1, 2}, SampleRate: 16000, NumChannels: 1, SamplesPerChannel: 1})
	receivePipelineFrame(t, pushed)

	select {
	case ev := <-errorEvents:
		t.Fatalf("ErrorEvents received %#v, want no error for canceled VAD push", ev)
	default:
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

func TestPipelineAgentVADLoopEndsActiveSpeechWhenStreamCloses(t *testing.T) {
	endpointing := &recordingPipelineEndpointing{}
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	pipeline := NewPipelineAgent(&fakePipelineVAD{}, nil, nil, nil, nil)
	pipeline.session = session

	pipeline.vadLoop(&fakePipelineVADStream{
		events: []*vad.VADEvent{{Type: vad.VADEventStartOfSpeech, Timestamp: 1.25}},
	})

	if got := session.UserState(); got != UserStateListening {
		t.Fatalf("UserState() after VAD stream close = %q, want listening", got)
	}
	if endpointing.startCount != 1 || endpointing.startAt != 1.25 {
		t.Fatalf("endpointing start = (%d, %.2f), want (1, 1.25)", endpointing.startCount, endpointing.startAt)
	}
	if endpointing.endCount != 1 {
		t.Fatalf("endpointing end calls = %d, want 1 after VAD stream close", endpointing.endCount)
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

func TestPipelineAgentLLMChatFailurePropagatesToRunResult(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("llm chat failed")
	l := &failingPipelineLLM{
		label: "test.LLM",
		err:   cause,
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()
	session.activity = NewAgentActivity(NewAgent("test"), session)

	result := NewRunResult(session.ChatCtx)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	result.WatchSpeechHandle(speech)

	agent.OnSpeechScheduled(context.Background(), speech)

	if err := result.Wait(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("RunResult Wait error = %v, want LLM chat error %v", err, cause)
	}
}

func TestPipelineAgentGeneratedReplyInterruptSuppressesCanceledLLMChatError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := fmt.Errorf("chat request: %w", context.Canceled)
	l := &failingPipelineLLM{err: cause}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no LLM chat error on interrupted cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentPreemptiveInterruptSuppressesCanceledLLMChatError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	l := &blockingCanceledPipelineLLM{started: make(chan struct{})}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechPreemptive(context.Background(), speech)
	}()

	select {
	case <-l.started:
	case <-time.After(time.Second):
		t.Fatal("preemptive LLM chat did not start")
	}
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnSpeechPreemptive did not return after interrupted LLM cancel")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no preemptive LLM error on interrupted cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentPreemptiveContextCancelSuppressesCanceledLLMChatError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	l := &blockingCanceledPipelineLLM{started: make(chan struct{})}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechPreemptive(ctx, speech)
	}()

	select {
	case <-l.started:
	case <-time.After(time.Second):
		t.Fatal("preemptive LLM chat did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnSpeechPreemptive did not return after context LLM cancel")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no preemptive LLM error on context cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentEmitsLLMErrorEventForStreamFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("llm stream failed")
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			err: cause,
		},
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
		if ev.Source != l {
			t.Fatalf("Source = %#v, want LLM source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive LLM stream error")
	}
}

func TestPipelineAgentLLMStreamFailurePropagatesToRunResult(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("llm stream failed")
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			err: cause,
		},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()
	session.activity = NewAgentActivity(NewAgent("test"), session)

	result := NewRunResult(session.ChatCtx)
	speech := NewSpeechHandle(true, DefaultInputDetails())
	result.WatchSpeechHandle(speech)

	agent.OnSpeechScheduled(context.Background(), speech)

	if err := result.Wait(context.Background()); !errors.Is(err, cause) {
		t.Fatalf("RunResult Wait error = %v, want LLM stream error %v", err, cause)
	}
}

func TestPipelineAgentGeneratedReplyInterruptSuppressesCanceledLLMStreamError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := fmt.Errorf("stream read: %w", context.Canceled)
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "partial"}},
			},
			err: cause,
		},
	}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no LLM error on interrupted cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentPreemptiveInterruptSuppressesCanceledTTSStartupError(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "cancel preemptive tts"}},
			},
		},
	}
	ttsSource := &blockingCanceledPipelineTTS{started: make(chan struct{})}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		PreemptiveGenerationPreemptiveTTS:    true,
		PreemptiveGenerationPreemptiveTTSSet: true,
	})
	agent := NewPipelineAgent(nil, nil, l, ttsSource, llm.NewChatContext())
	agent.session = session
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechPreemptive(context.Background(), speech)
	}()

	select {
	case <-ttsSource.started:
	case <-time.After(time.Second):
		t.Fatal("preemptive TTS stream did not start")
	}
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnSpeechPreemptive did not return after interrupted TTS cancel")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no preemptive TTS error on interrupted cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentPreemptiveContextCancelSuppressesCanceledTTSStartupError(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "cancel preemptive tts"}},
			},
		},
	}
	ttsSource := &blockingCanceledPipelineTTS{started: make(chan struct{})}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		PreemptiveGenerationPreemptiveTTS:    true,
		PreemptiveGenerationPreemptiveTTSSet: true,
	})
	agent := NewPipelineAgent(nil, nil, l, ttsSource, llm.NewChatContext())
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechPreemptive(ctx, speech)
	}()

	select {
	case <-ttsSource.started:
	case <-time.After(time.Second):
		t.Fatal("preemptive TTS stream did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnSpeechPreemptive did not return after context TTS cancel")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no preemptive TTS error on context cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentEmitsTTSErrorEventForSpeechSynthesisFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("tts stream failed")
	ttsSource := &fakePipelineTTS{streamErr: cause}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, llm.NewChatContext())
	agent.session = session
	speech := NewSpeechHandle(false, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	select {
	case ev := <-session.ErrorEvents():
		var ttsErr tts.TTSError
		if !errors.As(ev.Error, &ttsErr) {
			t.Fatalf("Error = %T, want tts.TTSError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ttsErr.Label != "fake" || ttsErr.Recoverable {
			t.Fatalf("TTSError = %#v, want fake unrecoverable error", ttsErr)
		}
		if ev.Source != ttsSource {
			t.Fatalf("Source = %#v, want TTS source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive TTS error")
	}
}

func TestPipelineAgentEmitsTTSErrorEventForSpeechStreamFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("tts next failed")
	ttsSource := &fakePipelineTTS{stream: &fakePipelineTTSStream{err: cause}}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, llm.NewChatContext())
	agent.session = session
	speech := NewSpeechHandle(false, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	select {
	case ev := <-session.ErrorEvents():
		var ttsErr tts.TTSError
		if !errors.As(ev.Error, &ttsErr) {
			t.Fatalf("Error = %T, want tts.TTSError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != ttsSource {
			t.Fatalf("Source = %#v, want TTS source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive TTS stream error")
	}
}

func TestPipelineAgentEmitsTTSErrorEventForPublishAudioFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("publish audio failed")
	ttsSource := &fakePipelineTTS{stream: &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte{0, 1},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}},
	}}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, llm.NewChatContext())
	agent.session = session
	agent.PublishAudio = func(context.Context, *model.AudioFrame) error {
		return cause
	}
	speech := NewSpeechHandle(false, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	select {
	case ev := <-session.ErrorEvents():
		var ttsErr tts.TTSError
		if !errors.As(ev.Error, &ttsErr) {
			t.Fatalf("Error = %T, want tts.TTSError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != ttsSource {
			t.Fatalf("Source = %#v, want TTS source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive publish audio error")
	}
}

func TestPipelineAgentStopsPublishingTTSAfterSpeechInterrupted(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ttsSource := &fakePipelineTTS{stream: &fakePipelineTTSStream{
		frames: []*model.AudioFrame{
			{
				Data:              []byte{1},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 1,
			},
			{
				Data:              []byte{2},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 1,
			},
		},
	}}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, chatCtx)
	agent.session = session
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}
	var published [][]byte
	agent.PublishAudio = func(_ context.Context, frame *model.AudioFrame) error {
		published = append(published, append([]byte(nil), frame.Data...))
		if len(published) == 1 {
			if err := speech.Interrupt(false); err != nil {
				t.Fatalf("Interrupt error = %v, want nil", err)
			}
		}
		return nil
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	if len(published) != 1 {
		t.Fatalf("published frames = %#v, want only first frame before interruption", published)
	}
	if got := published[0]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("first published frame = %#v, want frame 1", got)
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items = %#v, want committed assistant message after first audio frame", chatCtx.Items)
	}
	items := speech.ChatItems()
	if len(items) != 1 {
		t.Fatalf("speech.ChatItems = %#v, want committed assistant message after first audio frame", items)
	}
	msg, ok := items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("speech chat item = %T, want *llm.ChatMessage", items[0])
	}
	if msg.TextContent() != "hello" || !msg.Interrupted {
		t.Fatalf("speech chat message = %#v, want interrupted assistant text", msg)
	}
}

func TestPipelineAgentScheduledSayInterruptUsesSynchronizedTranscript(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:       200 * time.Millisecond,
			Interrupted:            true,
			SynchronizedTranscript: "heard words",
		},
	}
	session.SetAudioPlaybackController(playback)
	ttsSource := &fakePipelineTTS{stream: &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte{1},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}},
	}}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, chatCtx)
	agent.session = session
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "full words never heard"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "full words never heard"}},
	}
	agent.PublishAudio = func(context.Context, *model.AudioFrame) error {
		if err := speech.Interrupt(false); err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
		return nil
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1 after interrupted say", playback.clearCalls)
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items = %#v, want committed synchronized assistant text", chatCtx.Items)
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("chatCtx item = %T, want *llm.ChatMessage", chatCtx.Items[0])
	}
	if msg.TextContent() != "heard words" || !msg.Interrupted {
		t.Fatalf("chatCtx message = %#v, want interrupted synchronized transcript", msg)
	}
}

func TestPipelineAgentEmitsTTSErrorEventForChunkedSpeechSynthesisFailure(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("tts synthesize failed")
	ttsSource := &fakePipelineTTS{
		capabilities:  tts.TTSCapabilities{Streaming: false, AlignedTranscript: true},
		synthesizeErr: cause,
	}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, llm.NewChatContext())
	agent.session = session
	speech := NewSpeechHandle(false, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	select {
	case ev := <-session.ErrorEvents():
		var ttsErr tts.TTSError
		if !errors.As(ev.Error, &ttsErr) {
			t.Fatalf("Error = %T, want tts.TTSError", ev.Error)
		}
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
		if ev.Source != ttsSource {
			t.Fatalf("Source = %#v, want TTS source", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive chunked TTS error")
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
						PromptTokens:        7,
						PromptCachedTokens:  2,
						CacheCreationTokens: 3,
						CacheReadTokens:     4,
						CompletionTokens:    5,
						TotalTokens:         12,
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
		if metrics.PromptTokens != 7 || metrics.PromptCachedTokens != 9 || metrics.CompletionTokens != 5 || metrics.TotalTokens != 12 {
			t.Fatalf("token metrics = %#v, want prompt=7 cached=9 completion=5 total=12", metrics)
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
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{Content: "fallback answer"}},
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

	if len(l.calls) != 3 {
		t.Fatalf("LLM Chat calls = %d, want 3", len(l.calls))
	}
	if l.calls[0].ToolChoice != nil {
		t.Fatalf("first ToolChoice = %#v, want nil", l.calls[0].ToolChoice)
	}
	if l.calls[1].ToolChoice != nil {
		t.Fatalf("second ToolChoice = %#v, want nil before max step is exceeded", l.calls[1].ToolChoice)
	}
	if l.calls[2].ToolChoice != "none" {
		t.Fatalf("third ToolChoice = %#v, want none after max tool steps", l.calls[2].ToolChoice)
	}
	if tool.calls != 2 {
		t.Fatalf("tool executions = %d, want two tool calls before tool_choice none", tool.calls)
	}
}

func TestPipelineAgentExplicitZeroMaxToolStepsForcesNoToolsAfterInitialTool(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{},
		streams: []llm.LLMStream{
			toolCallStream("call_lookup_initial"),
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						Content: "final answer",
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup_after_limit",
							Arguments: `{}`,
						}},
					}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{
		MaxToolSteps:    0,
		MaxToolStepsSet: true,
	})
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
		t.Fatalf("second ToolChoice = %#v, want none after explicit zero max tool steps", l.calls[1].ToolChoice)
	}
	if tool.calls != 1 {
		t.Fatalf("tool executions = %d, want only initial tool call before tool_choice none", tool.calls)
	}
}

func TestPipelineAgentDefaultsMaxToolStepsToThree(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		streams: []llm.LLMStream{
			toolCallStream("call_lookup_1"),
			toolCallStream("call_lookup_2"),
			toolCallStream("call_lookup_3"),
			toolCallStream("call_lookup_4"),
			&fakeGenerationLLMStream{
				chunks: []*llm.ChatChunk{
					{Delta: &llm.ChoiceDelta{
						Content: "final answer",
						ToolCalls: []llm.FunctionToolCall{{
							Type:      "function",
							Name:      "lookup",
							CallID:    "call_lookup_after_default_limit",
							Arguments: `{}`,
						}},
					}},
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

	if len(l.calls) != 5 {
		t.Fatalf("LLM Chat calls = %d, want 5", len(l.calls))
	}
	for i := 0; i < 4; i++ {
		if l.calls[i].ToolChoice != nil {
			t.Fatalf("call %d ToolChoice = %#v, want nil", i+1, l.calls[i].ToolChoice)
		}
	}
	if l.calls[4].ToolChoice != "none" {
		t.Fatalf("fifth ToolChoice = %#v, want none after default max tool steps", l.calls[4].ToolChoice)
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
		if !strings.HasPrefix(ev.FunctionCalls[0].ID, "item_") || !strings.HasSuffix(ev.FunctionCalls[0].ID, "/fnc_0") {
			t.Fatalf("FunctionCall ID = %q, want generated item group suffix /fnc_0", ev.FunctionCalls[0].ID)
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

func TestPipelineAgentStopResponseDoesNotAppendToolCallOrGenerateReply(t *testing.T) {
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
					{Delta: &llm.ChoiceDelta{Content: "should not run"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", err: llm.StopResponse{}}}
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReply()

	if len(l.chatContexts) != 1 {
		t.Fatalf("LLM chat contexts = %d, want only initial call after StopResponse", len(l.chatContexts))
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no dangling function call after StopResponse", chatCtx.Items)
	}
	select {
	case ev := <-session.FunctionToolsExecutedEvents():
		if len(ev.FunctionCalls) != 1 || ev.FunctionCalls[0].CallID != "call_lookup" {
			t.Fatalf("FunctionCalls = %#v, want call_lookup event", ev.FunctionCalls)
		}
		if len(ev.FunctionCallOutputs) != 1 || ev.FunctionCallOutputs[0] != nil {
			t.Fatalf("FunctionCallOutputs = %#v, want nil output for StopResponse", ev.FunctionCallOutputs)
		}
		if ev.HasToolReply() {
			t.Fatal("HasToolReply() = true, want false for StopResponse")
		}
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive StopResponse event")
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

func TestPipelineAgentScheduledSaySkipsInterruptedSpeechBeforeSynthesis(t *testing.T) {
	chatCtx := llm.NewChatContext()
	ttsStream := &fakePipelineTTSStream{}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{}, &fakePipelineTTS{stream: ttsStream}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "unheard"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "unheard"}},
	}
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	if got := ttsStream.text.String(); got != "" {
		t.Fatalf("TTS text = %q, want no synthesis for interrupted speech", got)
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant commit for interrupted speech", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed chat items", speech.ChatItems())
	}
}

func TestPipelineAgentScheduledSayInterruptDuringSynthesisSuppressesTTSError(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	blockStream := &blockingPipelineTTSStream{started: make(chan struct{}), unblock: make(chan struct{})}
	ttsSource := &blockingPipelineTTS{stream: blockStream}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechScheduled(context.Background(), speech)
	}()

	<-blockStream.started
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}
	close(blockStream.unblock)
	<-done

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no TTS error on interrupt", ev.Error)
	default:
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant commit for interrupted unheard speech", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed chat items", speech.ChatItems())
	}
}

func TestPipelineAgentScheduledSayRealTTSErrorStillEmittedAfterInterruptSuppression(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	cause := errors.New("real tts failure")
	ttsSource := &fakePipelineTTS{streamErr: cause}
	agent := NewPipelineAgent(nil, nil, nil, ttsSource, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(false, DefaultInputDetails())
	speech.Generation.Text = "hello"
	speech.Generation.AssistantMessage = &llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello"}},
	}

	agent.OnSpeechScheduled(context.Background(), speech)

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive TTS error for real failure")
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant commit for failed say TTS", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed chat items for failed say TTS", speech.ChatItems())
	}
}

func TestPipelineAgentGeneratedReplyTTSErrorSkipsAssistantCommit(t *testing.T) {
	chatCtx := llm.NewChatContext()
	cause := errors.New("real generated reply tts failure")
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "unplayed answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{stream: &fakePipelineTTSStream{err: cause}}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want cause %v", ev.Error, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive generated reply TTS error")
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant commit for failed generated reply TTS", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed chat items for failed generated reply TTS", speech.ChatItems())
	}
}

func TestPipelineAgentGeneratedReplyInterruptSuppressesCanceledTTSError(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "cancel me"}},
			},
		},
	}
	blockStream := &blockingPipelineTTSStream{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
		err:     fmt.Errorf("Post %q: %w", "https://example.test/tts", context.Canceled),
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &blockingPipelineTTS{stream: blockStream}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})
	}()

	<-blockStream.started
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}
	close(blockStream.unblock)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generateReplyWithOptions did not return after interrupted canceled TTS stream")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no TTS error on interrupted cancellation", ev.Error)
	default:
	}
}

func TestPipelineAgentGeneratedReplyContextCancelSuppressesTTSError(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "cancel me"}},
			},
		},
	}
	blockStream := &blockingPipelineTTSStream{
		started: make(chan struct{}),
		unblock: make(chan struct{}),
		err:     fmt.Errorf("Post %q: %w", "https://example.test/tts", context.Canceled),
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &blockingPipelineTTS{stream: blockStream}, chatCtx)
	agent.session = session
	ctx, cancel := context.WithCancel(context.Background())
	agent.ctx = ctx
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})
	}()

	<-blockStream.started
	cancel()
	close(blockStream.unblock)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generateReplyWithOptions did not return after canceled context TTS stream")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no TTS error on context cancellation", ev.Error)
	default:
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant message for canceled unplayed speech", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestPipelineAgentScheduledReplyInterruptCancelsProviderContexts(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	l := &interruptAwarePipelineLLM{
		blocked:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	ttsSource := &interruptAwarePipelineTTS{
		blocked:  make(chan struct{}),
		canceled: make(chan struct{}),
	}
	agent := NewPipelineAgent(nil, nil, l, ttsSource, llm.NewChatContext())
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.OnSpeechScheduled(context.Background(), speech)
	}()

	select {
	case <-l.blocked:
	case <-time.After(time.Second):
		t.Fatal("LLM stream did not block waiting for cancellation")
	}
	select {
	case <-ttsSource.blocked:
	case <-time.After(time.Second):
		t.Fatal("TTS stream did not block waiting for cancellation")
	}
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	select {
	case <-l.canceled:
	case <-time.After(time.Second):
		t.Fatal("LLM context was not canceled after speech interrupt")
	}
	select {
	case <-ttsSource.canceled:
	case <-time.After(time.Second):
		t.Fatal("TTS context was not canceled after speech interrupt")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("OnSpeechScheduled did not return after speech interrupt canceled providers")
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no provider error on interrupted cancellation", ev.Error)
	default:
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

func TestPipelineAgentScheduledSayWaitsForPlayoutBeforeCommit(t *testing.T) {
	pipelineCtx := llm.NewChatContext()
	ttsStream := &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte{0, 1},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}},
	}
	playback := &fakePipelinePlaybackController{
		waitStarted: make(chan struct{}),
		releaseWait: make(chan struct{}),
		result:      AudioPlaybackResult{PlaybackPosition: 42 * time.Millisecond},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{}, &fakePipelineTTS{stream: ttsStream}, pipelineCtx)
	agent.session = session
	agent.ctx = context.Background()
	session.Assistant = agent
	activity := NewAgentActivity(NewAgent("test"), session)
	session.activity = activity
	go activity.schedulingTask()
	defer activity.Stop()
	agent.PublishAudio = func(context.Context, *model.AudioFrame) error { return nil }

	handle, err := session.Say(context.Background(), "wait for playout")
	if err != nil {
		t.Fatalf("Say error = %v, want nil", err)
	}

	select {
	case <-playback.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("WaitForPlayout did not start for scheduled say")
	}
	if len(pipelineCtx.Items) != 0 {
		t.Fatalf("pipeline chat context items = %#v, want no commit before playout finishes", pipelineCtx.Items)
	}
	if len(handle.ChatItems()) != 0 {
		t.Fatalf("handle chat items = %#v, want no committed item before playout finishes", handle.ChatItems())
	}
	close(playback.releaseWait)
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Wait(waitCtx); err != nil {
		t.Fatalf("scheduled say did not finish after playout release: %v", err)
	}
	if !playback.waitSawFlush {
		t.Fatal("WaitForPlayout observed unflushed playback, want flush before wait like reference audio_output.flush")
	}
	if len(pipelineCtx.Items) != 1 {
		t.Fatalf("pipeline chat context items = %#v, want committed say assistant message after playout", pipelineCtx.Items)
	}
}

func TestPipelineAgentInterruptedReplySkipsUnplayedAssistantText(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "unheard answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		result: AudioPlaybackResult{Interrupted: true},
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant message for interrupted unplayed speech", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestPipelineAgentInterruptedReplySkipsGeneratedTextWhenPlayoutWaitCanceled(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "unconfirmed generated text"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		err: context.Canceled,
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant message when interrupted playout wait is canceled", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestPipelineAgentInterruptedReplyCommitsSynchronizedTranscript(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "full answer the user did not hear"}},
			},
		},
	}
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{
			PlaybackPosition:       200 * time.Millisecond,
			Interrupted:            true,
			SynchronizedTranscript: "full answer",
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if playback.clearCalls != 1 {
		t.Fatalf("ClearBuffer calls = %d, want 1", playback.clearCalls)
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items length = %d, want committed partial assistant message", len(chatCtx.Items))
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("chatCtx item = %T, want *llm.ChatMessage", chatCtx.Items[0])
	}
	if msg.TextContent() != "full answer" || !msg.Interrupted {
		t.Fatalf("assistant message text/interrupted = %q/%v, want partial interrupted message", msg.TextContent(), msg.Interrupted)
	}
}

func TestPipelineAgentReplyWaitsForPlayoutBeforeCommit(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "played answer"}},
			},
		},
	}
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(&fakePipelinePlaybackController{
		waitStarted: waitStarted,
		releaseWait: releaseWait,
		result: AudioPlaybackResult{
			PlaybackPosition: 300 * time.Millisecond,
		},
	})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan struct{})
	go func() {
		defer close(done)
		agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})
	}()

	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("pipeline did not wait for audio playout before committing")
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant message before playout finishes", chatCtx.Items)
	}
	select {
	case <-done:
		t.Fatal("generateReplyWithOptions returned before playback was released")
	default:
	}

	close(releaseWait)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("generateReplyWithOptions did not finish after playback was released")
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items length = %d, want committed assistant message after playout", len(chatCtx.Items))
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.TextContent() != "played answer" || msg.Interrupted {
		t.Fatalf("assistant message = %#v, want uninterrupted played answer", chatCtx.Items[0])
	}
}

func TestPipelineAgentReplyWithoutTTSAudioCommitsAssistantText(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "text only answer"}},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, nil, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items length = %d, want committed assistant message without TTS", len(chatCtx.Items))
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.TextContent() != "text only answer" || msg.Interrupted {
		t.Fatalf("assistant message = %#v, want uninterrupted text-only answer", chatCtx.Items[0])
	}
	if len(speech.ChatItems()) != 1 {
		t.Fatalf("speech.ChatItems length = %d, want committed assistant message", len(speech.ChatItems()))
	}
	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("ErrorEvents received unexpected error = %v, want no TTS error without audio output", ev.Error)
	default:
	}
}

func TestPipelineAgentReplySplitsTTSOnLLMFlush(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "first segment"}},
				{Delta: &llm.ChoiceDelta{Flush: true}},
				{Delta: &llm.ChoiceDelta{Content: "second segment"}},
			},
		},
	}
	firstTTS := &fakePipelineTTSStream{}
	secondTTS := &fakePipelineTTSStream{}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{streams: []*fakePipelineTTSStream{firstTTS, secondTTS}}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: NewSpeechHandle(true, DefaultInputDetails())})

	if firstTTS.text.String() != "first segment" {
		t.Fatalf("first TTS text = %q, want first segment", firstTTS.text.String())
	}
	if secondTTS.text.String() != "second segment" {
		t.Fatalf("second TTS text = %q, want second segment", secondTTS.text.String())
	}
	if len(chatCtx.Items) != 1 {
		t.Fatalf("chatCtx.Items length = %d, want committed assistant message", len(chatCtx.Items))
	}
	msg, ok := chatCtx.Items[0].(*llm.ChatMessage)
	if !ok || msg.TextContent() != "first segmentsecond segment" {
		t.Fatalf("assistant message = %#v, want full generated text", chatCtx.Items[0])
	}
}

func TestPipelineAgentInterruptedTextOnlyReplySkipsAssistantText(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	agent := NewPipelineAgent(nil, nil, &fakeGenerationLLM{}, nil, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	speech := NewSpeechHandle(true, DefaultInputDetails())
	textCh := make(chan string)
	close(textCh)
	functionCh := make(chan *llm.FunctionToolCall)
	close(functionCh)
	speech.setPrecomputedLLMGeneration(&LLMGenerationData{
		TextCh:        textCh,
		FunctionCh:    functionCh,
		GeneratedText: "unheard text only answer",
		ID:            "item_text_only",
	})
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt error = %v, want nil", err)
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: speech})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chatCtx.Items = %#v, want no assistant message for interrupted text-only reply", chatCtx.Items)
	}
	if len(speech.ChatItems()) != 0 {
		t.Fatalf("speech.ChatItems = %#v, want no committed assistant message", speech.ChatItems())
	}
}

func TestPipelineAgentReplyFlushesPlaybackAfterTTSEOFBeforeWaiting(t *testing.T) {
	chatCtx := llm.NewChatContext()
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Content: "played answer"}},
			},
		},
	}
	ttsStream := &fakePipelineTTSStream{
		frames: []*model.AudioFrame{{
			Data:              []byte{0, 1},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}},
	}
	playback := &fakePipelinePlaybackController{
		result: AudioPlaybackResult{PlaybackPosition: 42 * time.Millisecond},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	session.SetAudioPlaybackController(playback)
	agent := NewPipelineAgent(nil, nil, l, &fakePipelineTTS{stream: ttsStream}, chatCtx)
	agent.session = session
	agent.ctx = context.Background()
	var published int
	agent.PublishAudio = func(context.Context, *model.AudioFrame) error {
		published++
		return nil
	}

	agent.generateReplyWithOptions(pipelineReplyOptions{SpeechHandle: NewSpeechHandle(true, DefaultInputDetails())})

	if published != 1 {
		t.Fatalf("published audio frames = %d, want 1", published)
	}
	if playback.flushCalls != 1 {
		t.Fatalf("Flush calls = %d, want 1 after TTS EOF", playback.flushCalls)
	}
	if !playback.waitSawFlush {
		t.Fatal("WaitForPlayout observed unflushed playback, want flush before wait like reference audio_output.flush")
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
	model         string
	provider      string
	stream        *fakePipelineTTSStream
	streams       []*fakePipelineTTSStream
	streamErr     error
	synthesizeErr error
	capabilities  tts.TTSCapabilities
}

func (f *fakePipelineTTS) Label() string { return "fake" }

func (f *fakePipelineTTS) Model() string { return f.model }

func (f *fakePipelineTTS) Provider() string { return f.provider }

func (f *fakePipelineTTS) Capabilities() tts.TTSCapabilities {
	if f.capabilities != (tts.TTSCapabilities{}) {
		return f.capabilities
	}
	return tts.TTSCapabilities{Streaming: true}
}

func (f *fakePipelineTTS) SampleRate() int { return 24000 }

func (f *fakePipelineTTS) NumChannels() int { return 1 }

func (f *fakePipelineTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	if f.synthesizeErr != nil {
		return nil, f.synthesizeErr
	}
	return nil, nil
}

func (f *fakePipelineTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	if len(f.streams) > 0 {
		stream := f.streams[0]
		f.streams = f.streams[1:]
		return stream, nil
	}
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

type blockingCanceledPipelineLLM struct {
	started chan struct{}
}

func (b *blockingCanceledPipelineLLM) Chat(ctx context.Context, _ *llm.ChatContext, _ ...llm.ChatOption) (llm.LLMStream, error) {
	close(b.started)
	<-ctx.Done()
	return nil, fmt.Errorf("chat request: %w", ctx.Err())
}

type blockingCanceledPipelineTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter
	started chan struct{}
}

func (b *blockingCanceledPipelineTTS) Label() string { return "blocking-canceled-tts" }

func (b *blockingCanceledPipelineTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (b *blockingCanceledPipelineTTS) SampleRate() int { return 24000 }

func (b *blockingCanceledPipelineTTS) NumChannels() int { return 1 }

func (b *blockingCanceledPipelineTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, errors.New("Synthesize should not be called for streaming TTS")
}

func (b *blockingCanceledPipelineTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	close(b.started)
	<-ctx.Done()
	return nil, fmt.Errorf("Post %q: %w", "https://example.test/tts", ctx.Err())
}

type interruptAwarePipelineLLM struct {
	blocked  chan struct{}
	canceled chan struct{}
}

func (i *interruptAwarePipelineLLM) Chat(ctx context.Context, _ *llm.ChatContext, _ ...llm.ChatOption) (llm.LLMStream, error) {
	return &interruptAwarePipelineLLMStream{
		ctx:      ctx,
		blocked:  i.blocked,
		canceled: i.canceled,
	}, nil
}

type interruptAwarePipelineLLMStream struct {
	ctx      context.Context
	blocked  chan struct{}
	canceled chan struct{}
	index    int
}

func (i *interruptAwarePipelineLLMStream) Next() (*llm.ChatChunk, error) {
	if i.index == 0 {
		i.index++
		return &llm.ChatChunk{Delta: &llm.ChoiceDelta{Content: "cancel me"}}, nil
	}
	select {
	case <-i.blocked:
	default:
		close(i.blocked)
	}
	<-i.ctx.Done()
	select {
	case <-i.canceled:
	default:
		close(i.canceled)
	}
	return nil, fmt.Errorf("llm stream: %w", i.ctx.Err())
}

func (i *interruptAwarePipelineLLMStream) Close() error { return nil }

type interruptAwarePipelineTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter
	blocked  chan struct{}
	canceled chan struct{}
}

func (i *interruptAwarePipelineTTS) Label() string { return "interrupt-aware-tts" }

func (i *interruptAwarePipelineTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}

func (i *interruptAwarePipelineTTS) SampleRate() int { return 24000 }

func (i *interruptAwarePipelineTTS) NumChannels() int { return 1 }

func (i *interruptAwarePipelineTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, errors.New("Synthesize should not be called for streaming TTS")
}

func (i *interruptAwarePipelineTTS) Stream(ctx context.Context) (tts.SynthesizeStream, error) {
	return &interruptAwarePipelineTTSStream{
		ctx:      ctx,
		blocked:  i.blocked,
		canceled: i.canceled,
	}, nil
}

type interruptAwarePipelineTTSStream struct {
	ctx      context.Context
	blocked  chan struct{}
	canceled chan struct{}
	index    int
}

func (i *interruptAwarePipelineTTSStream) PushText(string) error { return nil }

func (i *interruptAwarePipelineTTSStream) Flush() error { return nil }

func (i *interruptAwarePipelineTTSStream) Close() error { return nil }

func (i *interruptAwarePipelineTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if i.index == 0 {
		i.index++
		return &tts.SynthesizedAudio{Frame: &model.AudioFrame{
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 240,
			Data:              []byte{1, 2, 3, 4},
		}}, nil
	}
	select {
	case <-i.blocked:
	default:
		close(i.blocked)
	}
	<-i.ctx.Done()
	select {
	case <-i.canceled:
	default:
		close(i.canceled)
	}
	return nil, fmt.Errorf("tts stream: %w", i.ctx.Err())
}

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
	err              error
}

type blockingPipelineTTSStream struct {
	started chan struct{}
	unblock chan struct{}
	err     error
}

func (b *blockingPipelineTTSStream) PushText(string) error { return nil }
func (b *blockingPipelineTTSStream) Flush() error          { return nil }
func (b *blockingPipelineTTSStream) Close() error          { return nil }
func (b *blockingPipelineTTSStream) Next() (*tts.SynthesizedAudio, error) {
	if b.started != nil {
		select {
		case <-b.started:
		default:
			close(b.started)
			b.started = nil
		}
	}
	<-b.unblock
	if b.err != nil {
		return nil, b.err
	}
	return nil, io.EOF
}

type blockingPipelineTTS struct {
	tts.MetricsEmitter
	tts.ErrorEmitter
	stream *blockingPipelineTTSStream
}

func (b *blockingPipelineTTS) Label() string    { return "fake-blocking" }
func (b *blockingPipelineTTS) Model() string    { return "" }
func (b *blockingPipelineTTS) Provider() string { return "" }
func (b *blockingPipelineTTS) Capabilities() tts.TTSCapabilities {
	return tts.TTSCapabilities{Streaming: true}
}
func (b *blockingPipelineTTS) SampleRate() int  { return 24000 }
func (b *blockingPipelineTTS) NumChannels() int { return 1 }
func (b *blockingPipelineTTS) Synthesize(context.Context, string) (tts.ChunkedStream, error) {
	return nil, nil
}
func (b *blockingPipelineTTS) Stream(context.Context) (tts.SynthesizeStream, error) {
	return b.stream, nil
}

type fakePipelinePlaybackController struct {
	clearCalls int
	flushCalls int
	result     AudioPlaybackResult
	err        error

	waitStarted  chan struct{}
	releaseWait  chan struct{}
	waitSawFlush bool
}

func (f *fakePipelinePlaybackController) ClearBuffer() {
	f.clearCalls++
}

func (f *fakePipelinePlaybackController) Flush() {
	f.flushCalls++
}

func (f *fakePipelinePlaybackController) WaitForPlayout(context.Context) (AudioPlaybackResult, error) {
	f.waitSawFlush = f.flushCalls > 0
	if f.waitStarted != nil {
		close(f.waitStarted)
		f.waitStarted = nil
	}
	if f.releaseWait != nil {
		<-f.releaseWait
	}
	return f.result, f.err
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
	if f.err != nil {
		err := f.err
		f.err = nil
		return nil, err
	}
	return nil, io.EOF
}

type fakePipelineSTT struct {
	stt.MetricsEmitter
	stt.ErrorEmitter

	stream    *fakePipelineRecognizeStream
	streamErr error
}

func (f *fakePipelineSTT) Label() string { return "fake-stt" }

func (f *fakePipelineSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}

func (f *fakePipelineSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	if f.stream != nil {
		return f.stream, nil
	}
	return &fakePipelineRecognizeStream{}, nil
}

func (f *fakePipelineSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type fakePipelineSTTWithSampleRate struct {
	fakePipelineSTT
	inputSampleRate uint32
}

func (f *fakePipelineSTTWithSampleRate) InputSampleRate() uint32 { return f.inputSampleRate }

type queuedPipelineSTT struct {
	stt.MetricsEmitter
	stt.ErrorEmitter
	streams []stt.RecognizeStream
}

func (q *queuedPipelineSTT) Label() string { return "queued-stt" }

func (q *queuedPipelineSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true}
}

func (q *queuedPipelineSTT) Stream(context.Context, string) (stt.RecognizeStream, error) {
	if len(q.streams) == 0 {
		return nil, io.EOF
	}
	stream := q.streams[0]
	q.streams = q.streams[1:]
	return stream, nil
}

func (q *queuedPipelineSTT) Recognize(context.Context, []*model.AudioFrame, string) (*stt.SpeechEvent, error) {
	return nil, nil
}

type fakePipelineVAD struct {
	metricsHandlers []vad.VADMetricsHandler
	stream          *fakePipelineVADStream
	streamErr       error
}

func (f *fakePipelineVAD) Label() string { return "fake-vad" }

func (f *fakePipelineVAD) Model() string { return "fake-vad" }

func (f *fakePipelineVAD) Provider() string { return "fake" }

func (f *fakePipelineVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{}
}

func (f *fakePipelineVAD) OnMetricsCollected(handler vad.VADMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	f.metricsHandlers = append(f.metricsHandlers, handler)
	index := len(f.metricsHandlers) - 1
	var once sync.Once
	return func() {
		once.Do(func() {
			f.metricsHandlers = append(f.metricsHandlers[:index], f.metricsHandlers[index+1:]...)
		})
	}
}

func (f *fakePipelineVAD) EmitMetricsCollected(metrics *telemetry.VADMetrics) {
	for _, handler := range f.metricsHandlers {
		handler(metrics)
	}
}

func (f *fakePipelineVAD) Stream(context.Context) (vad.VADStream, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	if f.stream != nil {
		return f.stream, nil
	}
	return &fakePipelineVADStream{}, nil
}

type fakePipelineVADStream struct {
	events     []*vad.VADEvent
	index      int
	err        error
	pushErr    error
	frames     []*model.AudioFrame
	pushedCh   chan *model.AudioFrame
	closedCh   chan struct{}
	closeOnce  sync.Once
	flushCount int
}

func (f *fakePipelineVADStream) PushFrame(frame *model.AudioFrame) error {
	f.frames = append(f.frames, frame)
	if f.pushedCh != nil {
		f.pushedCh <- frame
	}
	return f.pushErr
}

func (f *fakePipelineVADStream) Flush() error {
	f.flushCount++
	return nil
}

func (f *fakePipelineVADStream) EndInput() error { return nil }

func (f *fakePipelineVADStream) Close() error {
	if f.closedCh != nil {
		f.closeOnce.Do(func() { close(f.closedCh) })
	}
	return nil
}

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

type updateInstructionsPipelineTool struct {
	instructions string
}

func (u updateInstructionsPipelineTool) ID() string { return "update_instructions" }

func (u updateInstructionsPipelineTool) Name() string { return "update_instructions" }

func (u updateInstructionsPipelineTool) Description() string { return "" }

func (u updateInstructionsPipelineTool) Parameters() map[string]any { return nil }

func (u updateInstructionsPipelineTool) Execute(ctx context.Context, _ string) (string, error) {
	runCtx := GetRunContext(ctx)
	if runCtx == nil || runCtx.Session == nil || runCtx.Session.Agent == nil {
		return "", errors.New("missing run context")
	}
	if err := runCtx.Session.Agent.GetAgent().UpdateInstructions(ctx, u.instructions); err != nil {
		return "", err
	}
	return "updated", nil
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

func instructionMessageFromContext(t *testing.T, chatCtx *llm.ChatContext) *llm.ChatMessage {
	t.Helper()
	for _, item := range chatCtx.Items {
		if item.GetID() != agentInstructionsMessageID {
			continue
		}
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("instruction item = %T, want *llm.ChatMessage", item)
		}
		return msg
	}
	t.Fatalf("chat context items = %#v, want instruction message", chatCtx.Items)
	return nil
}

func assertAssistantMetricMetadata(t *testing.T, metrics map[string]any, key, model, provider string) {
	t.Helper()

	got, ok := metrics[key].(map[string]any)
	if !ok {
		t.Fatalf("assistant Metrics[%s] = %#v, want metadata map", key, metrics[key])
	}
	if got["model_name"] != model || got["model_provider"] != provider {
		t.Fatalf("assistant Metrics[%s] = %#v, want model/provider %s/%s", key, got, model, provider)
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

func receiveUserInputTranscribedEvent(t *testing.T, events <-chan UserInputTranscribedEvent) UserInputTranscribedEvent {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive transcript")
	}
	return UserInputTranscribedEvent{}
}

func receivePipelineFrame(t *testing.T, ch <-chan *model.AudioFrame) *model.AudioFrame {
	t.Helper()

	select {
	case frame := <-ch:
		return frame
	case <-time.After(time.Second):
		t.Fatal("pipeline stream did not receive audio frame")
	}
	return nil
}

func receivePipelineClosed(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s stream was not closed", name)
	}
}

type erroringPipelineLLM struct {
	err error
}

func (e erroringPipelineLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return nil, e.err
}

type fakePipelineRecognizeStream struct {
	events     []*stt.SpeechEvent
	index      int
	err        error
	pushErr    error
	frames     []*model.AudioFrame
	pushedCh   chan *model.AudioFrame
	closedCh   chan struct{}
	flushCount int
	closeOnce  sync.Once
}

func (f *fakePipelineRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	f.frames = append(f.frames, frame)
	if f.pushedCh != nil {
		f.pushedCh <- frame
	}
	return f.pushErr
}

func (f *fakePipelineRecognizeStream) Flush() error {
	f.flushCount++
	return nil
}

func (f *fakePipelineRecognizeStream) Close() error {
	if f.closedCh != nil {
		f.closeOnce.Do(func() { close(f.closedCh) })
	}
	return nil
}

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
