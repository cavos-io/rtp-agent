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
					{Delta: &llm.ChoiceDelta{Content: "final answer"}},
				},
			},
		},
	}
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{MaxToolSteps: 1})
	session.Tools = []llm.Tool{&fakeGenerationTool{name: "lookup", result: "done"}}
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
	stream *fakePipelineTTSStream
}

func (f *fakePipelineTTS) Label() string { return "fake" }

func (f *fakePipelineTTS) Capabilities() tts.TTSCapabilities {
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

type fakePipelineTTSStream struct {
	text   strings.Builder
	frames []*model.AudioFrame
	next   int
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
		f.next++
		return &tts.SynthesizedAudio{Frame: frame}, nil
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

type fakePipelineVAD struct{}

func (f *fakePipelineVAD) Label() string { return "fake-vad" }

func (f *fakePipelineVAD) Model() string { return "fake-vad" }

func (f *fakePipelineVAD) Provider() string { return "fake" }

func (f *fakePipelineVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{}
}

func (f *fakePipelineVAD) OnMetricsCollected(vad.VADMetricsHandler) {}

func (f *fakePipelineVAD) Stream(context.Context) (vad.VADStream, error) {
	return &fakePipelineVADStream{}, nil
}

type fakePipelineVADStream struct {
	err error
}

func (f *fakePipelineVADStream) PushFrame(*model.AudioFrame) error { return nil }

func (f *fakePipelineVADStream) Flush() error { return nil }

func (f *fakePipelineVADStream) EndInput() error { return nil }

func (f *fakePipelineVADStream) Close() error { return nil }

func (f *fakePipelineVADStream) Next() (*vad.VADEvent, error) {
	if f.err != nil {
		err := f.err
		f.err = nil
		return nil, err
	}
	return nil, io.EOF
}

type blockingPipelineTool struct {
	name    string
	started chan struct{}
	release chan struct{}
	once    sync.Once
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
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.AgentState
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
