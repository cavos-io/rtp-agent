package agent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
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
