package agent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/tts"
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
	case <-time.After(time.Second):
		t.Fatal("FunctionToolsExecutedEvents did not receive event")
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
	text strings.Builder
}

func (f *fakePipelineTTSStream) PushText(text string) error {
	_, _ = f.text.WriteString(text)
	return nil
}

func (f *fakePipelineTTSStream) Flush() error { return nil }

func (f *fakePipelineTTSStream) Close() error { return nil }

func (f *fakePipelineTTSStream) Next() (*tts.SynthesizedAudio, error) {
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

type fakePipelineRecognizeStream struct {
	events []*stt.SpeechEvent
	index  int
}

func (f *fakePipelineRecognizeStream) PushFrame(*model.AudioFrame) error { return nil }

func (f *fakePipelineRecognizeStream) Flush() error { return nil }

func (f *fakePipelineRecognizeStream) Close() error { return nil }

func (f *fakePipelineRecognizeStream) Next() (*stt.SpeechEvent, error) {
	if f.index >= len(f.events) {
		return nil, io.EOF
	}
	ev := f.events[f.index]
	f.index++
	return ev, nil
}
