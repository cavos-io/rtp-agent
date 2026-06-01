package agent

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestPerformLLMInferenceIgnoresNonFunctionToolCalls(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{ToolCalls: []llm.FunctionToolCall{
					{Type: "custom", Name: "ignored", CallID: "call_ignored"},
					{Type: "function", Name: "lookup", CallID: "call_lookup"},
				}}},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}

	got := drainFunctionCalls(data.FunctionCh)
	if len(got) != 1 {
		t.Fatalf("len(FunctionCh) = %d, want 1 function tool call", len(got))
	}
	if got[0].Name != "lookup" || got[0].CallID != "call_lookup" {
		t.Fatalf("function call = %#v, want lookup/call_lookup", got[0])
	}
	if len(data.GeneratedFunctions) != 1 {
		t.Fatalf("len(GeneratedFunctions) = %d, want 1", len(data.GeneratedFunctions))
	}
	if data.GeneratedFunctions[0].Name != "lookup" || data.GeneratedFunctions[0].CallID != "call_lookup" {
		t.Fatalf("GeneratedFunctions[0] = %#v, want lookup/call_lookup", data.GeneratedFunctions[0])
	}
}

func TestPerformLLMInferenceTracksGeneratedExtra(t *testing.T) {
	l := &fakeGenerationLLM{
		stream: &fakeGenerationLLMStream{
			chunks: []*llm.ChatChunk{
				{Delta: &llm.ChoiceDelta{Extra: map[string]any{
					"trace_id": "first",
					"score":    1,
				}}},
				{Delta: &llm.ChoiceDelta{Extra: map[string]any{
					"trace_id": "second",
					"model":    "test-model",
				}}},
			},
		},
	}

	data, err := PerformLLMInference(context.Background(), l, llm.NewChatContext(), nil)
	if err != nil {
		t.Fatalf("PerformLLMInference error = %v, want nil", err)
	}

	drainStrings(data.TextCh)
	if got := data.GeneratedExtra["trace_id"]; got != "second" {
		t.Fatalf("GeneratedExtra[trace_id] = %#v, want second", got)
	}
	if got := data.GeneratedExtra["score"]; got != 1 {
		t.Fatalf("GeneratedExtra[score] = %#v, want 1", got)
	}
	if got := data.GeneratedExtra["model"]; got != "test-model" {
		t.Fatalf("GeneratedExtra[model] = %#v, want test-model", got)
	}
}

func TestPerformToolExecutionsUsesToolErrorMessage(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  llm.NewToolError("visible failure"),
	})

	if output.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want error output")
	}
	if !output.FncCallOut.IsError || output.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible ToolError output", output.FncCallOut)
	}
}

func TestPerformToolExecutionsMasksInternalErrors(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  errors.New("database password leaked"),
	})

	if output.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want error output")
	}
	if !output.FncCallOut.IsError || output.FncCallOut.Output != "An internal error occurred" {
		t.Fatalf("FncCallOut = %#v, want masked internal error", output.FncCallOut)
	}
}

func TestPerformToolExecutionsSuppressesOutputForStopResponse(t *testing.T) {
	output := executeOneToolCall(t, &fakeGenerationTool{
		name: "lookup",
		err:  llm.StopResponse{},
	})

	if output.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for StopResponse", output.FncCallOut)
	}
	if output.RawError == nil {
		t.Fatal("RawError = nil, want StopResponse")
	}
}

func executeOneToolCall(t *testing.T, tool llm.Tool) ToolExecutionOutput {
	t.Helper()

	toolCtx := llm.NewToolContext([]interface{}{tool})
	functionCh := make(chan *llm.FunctionToolCall, 1)
	functionCh <- &llm.FunctionToolCall{
		Name:      tool.Name(),
		CallID:    "call_lookup",
		Arguments: `{}`,
	}
	close(functionCh)

	outCh := PerformToolExecutions(context.Background(), functionCh, toolCtx)
	output, ok := <-outCh
	if !ok {
		t.Fatal("PerformToolExecutions closed without output")
	}
	return output
}

type fakeGenerationTool struct {
	name   string
	result string
	err    error
}

func (f *fakeGenerationTool) ID() string { return f.name }

func (f *fakeGenerationTool) Name() string { return f.name }

func (f *fakeGenerationTool) Description() string { return "" }

func (f *fakeGenerationTool) Parameters() map[string]any { return nil }

func (f *fakeGenerationTool) Execute(context.Context, string) (string, error) {
	return f.result, f.err
}

type fakeGenerationLLM struct {
	stream llm.LLMStream
}

func (f *fakeGenerationLLM) Chat(context.Context, *llm.ChatContext, ...llm.ChatOption) (llm.LLMStream, error) {
	return f.stream, nil
}

type fakeGenerationLLMStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (f *fakeGenerationLLMStream) Next() (*llm.ChatChunk, error) {
	if f.index >= len(f.chunks) {
		return nil, io.EOF
	}
	chunk := f.chunks[f.index]
	f.index++
	return chunk, nil
}

func (f *fakeGenerationLLMStream) Close() error { return nil }

func drainFunctionCalls(ch <-chan *llm.FunctionToolCall) []*llm.FunctionToolCall {
	calls := make([]*llm.FunctionToolCall, 0)
	for call := range ch {
		calls = append(calls, call)
	}
	return calls
}

func drainStrings(ch <-chan string) []string {
	values := make([]string, 0)
	for value := range ch {
		values = append(values, value)
	}
	return values
}
