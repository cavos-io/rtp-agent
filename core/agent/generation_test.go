package agent

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type mockLLM struct {
	chunks []*llm.ChatChunk
}

func (m *mockLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return &mockLLMStream{chunks: m.chunks}, nil
}

type mockLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (s *mockLLMStream) Next() (*llm.ChatChunk, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	ch := s.chunks[s.index]
	s.index++
	return ch, nil
}

func (s *mockLLMStream) Close() error { return nil }

func TestPerformLLMInferenceReassemblesStreamedToolCalls(t *testing.T) {
	model := &mockLLM{
		chunks: []*llm.ChatChunk{
			{
				ID: "c1",
				Delta: &llm.ChoiceDelta{
					Content: "hello",
				},
			},
			{
				ID: "c2",
				Delta: &llm.ChoiceDelta{
					ToolCalls: []llm.FunctionToolCall{
						{
							CallID:    "call_1",
							Name:      "get_weather",
							Arguments: "{\"city\":\"",
							Extra:     map[string]any{"index": 0},
						},
					},
				},
			},
			{
				ID: "c3",
				Delta: &llm.ChoiceDelta{
					ToolCalls: []llm.FunctionToolCall{
						{
							Arguments: "Jakarta\"}",
							Extra:     map[string]any{"index": 0},
						},
					},
				},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), model, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("perform llm inference failed: %v", err)
	}

	for range data.TextCh {
	}

	var toolCalls []*llm.FunctionToolCall
	for call := range data.FunctionCh {
		toolCalls = append(toolCalls, call)
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected tool name: %s", toolCalls[0].Name)
	}
	if toolCalls[0].CallID != "call_1" {
		t.Fatalf("unexpected call id: %s", toolCalls[0].CallID)
	}
	if toolCalls[0].Arguments != "{\"city\":\"Jakarta\"}" {
		t.Fatalf("unexpected merged args: %s", toolCalls[0].Arguments)
	}
}

type executingTool struct{}

func (executingTool) ID() string          { return "t1" }
func (executingTool) Name() string        { return "echo" }
func (executingTool) Description() string { return "echo tool" }
func (executingTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (executingTool) Execute(ctx context.Context, args string) (string, error) { return args, nil }

func TestPerformToolExecutionsValidatesJSONArguments(t *testing.T) {
	toolCtx := llm.NewToolContext([]interface{}{executingTool{}})
	in := make(chan *llm.FunctionToolCall, 1)
	in <- &llm.FunctionToolCall{
		CallID:    "call_1",
		Name:      "echo",
		Arguments: "{invalid",
	}
	close(in)

	out := PerformToolExecutions(context.Background(), in, toolCtx)
	result, ok := <-out
	if !ok {
		t.Fatalf("expected tool execution output")
	}

	if result.RawError == nil {
		t.Fatalf("expected argument validation error")
	}
	if result.FncCallOut == nil || !result.FncCallOut.IsError {
		t.Fatalf("expected errored function call output")
	}
}
