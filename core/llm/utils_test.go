package llm

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestStripThinkingTokensTracksHiddenChunks(t *testing.T) {
	thinking := false

	if got, ok := StripThinkingTokens("hello", &thinking); !ok || got != "hello" || thinking {
		t.Fatalf("plain content = (%q, %v, thinking=%v), want visible hello", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("<think>", &thinking); !ok || got != "" || !thinking {
		t.Fatalf("think start = (%q, %v, thinking=%v), want visible empty and thinking", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("hidden reasoning", &thinking); ok || got != "" || !thinking {
		t.Fatalf("hidden chunk = (%q, %v, thinking=%v), want suppressed and thinking", got, ok, thinking)
	}

	if got, ok := StripThinkingTokens("</think>visible", &thinking); !ok || got != "visible" || thinking {
		t.Fatalf("think end = (%q, %v, thinking=%v), want visible content and not thinking", got, ok, thinking)
	}
}

func TestParseFunctionArgumentsParsesJSONObject(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","limit":3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want parsed city and limit", args)
	}
}

func TestParseFunctionArgumentsUnwrapsNestedJSONString(t *testing.T) {
	args, err := ParseFunctionArguments(`"{\"city\":\"Paris\"}"`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("args = %#v, want nested JSON object", args)
	}
}

func TestParseFunctionArgumentsRepairsLeakedTemplateTokens(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris"}<|im_end|>`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("args = %#v, want repaired city", args)
	}
}

func TestParseFunctionArgumentsDropsListItemsEmptiedByTemplateRepair(t *testing.T) {
	args, err := ParseFunctionArguments(`{"tags":["<|im_start|>","urgent"]}<|im_end|>`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	tags, ok := args["tags"].([]any)
	if !ok {
		t.Fatalf("tags = %#v, want []any", args["tags"])
	}
	if len(tags) != 1 || tags[0] != "urgent" {
		t.Fatalf("tags = %#v, want only urgent after dropping empty repaired token", tags)
	}
}

func TestParseFunctionArgumentsRepairsTrailingCommas(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","limit":3,}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired city and limit", args)
	}
}

func TestParseFunctionArgumentsRepairsMissingClosingDelimiter(t *testing.T) {
	args, err := ParseFunctionArguments(`{"city":"Paris","tags":["metro","food"]`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" {
		t.Fatalf("city = %#v, want Paris", args["city"])
	}
	tags, ok := args["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "metro" || tags[1] != "food" {
		t.Fatalf("tags = %#v, want metro and food", args["tags"])
	}
}

func TestParseFunctionArgumentsRepairsUnquotedObjectKeys(t *testing.T) {
	args, err := ParseFunctionArguments(`{city:"Paris",limit:3}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["limit"] != float64(3) {
		t.Fatalf("args = %#v, want repaired city and limit", args)
	}
}

func TestParseFunctionArgumentsRepairsSingleQuotedValues(t *testing.T) {
	args, err := ParseFunctionArguments(`{'city':'Paris','country':'FR'}`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if args["city"] != "Paris" || args["country"] != "FR" {
		t.Fatalf("args = %#v, want repaired city and country", args)
	}
}

func TestParseFunctionArgumentsTreatsNullAsEmptyObject(t *testing.T) {
	args, err := ParseFunctionArguments(`null`)
	if err != nil {
		t.Fatalf("ParseFunctionArguments() error = %v", err)
	}

	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty map", args)
	}
}

func TestParseFunctionArgumentsRejectsNonObject(t *testing.T) {
	if _, err := ParseFunctionArguments(`["Paris"]`); err == nil {
		t.Fatal("ParseFunctionArguments(array) error = nil, want error")
	}
	if _, err := ParseFunctionArguments(`"not json object"`); err == nil {
		t.Fatal("ParseFunctionArguments(string) error = nil, want error")
	}
}

func TestMakeFunctionCallOutputUsesToolErrorMessage(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, NewToolError("visible failure"))

	if result.FncCall.CallID != call.CallID || result.FncCall.Name != call.Name || result.FncCall.Arguments != call.Arguments {
		t.Fatalf("FncCall = %#v, want original call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want visible tool error output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible error output", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want original error")
	}
}

func TestMakeFunctionCallOutputSuppressesStopResponse(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, StopResponse{})

	if result.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for StopResponse", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want StopResponse")
	}
}

func TestMakeFunctionCallOutputMasksInternalErrors(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, nil, errors.New("database password leaked"))

	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want masked internal error output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "An internal error occurred" {
		t.Fatalf("FncCallOut = %#v, want masked internal error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want original error")
	}
}

func TestMakeFunctionCallOutputStringifiesValidOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	result := MakeFunctionCallOutput(call, 7, nil)

	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want successful output")
	}
	if result.FncCallOut.IsError || result.FncCallOut.Output != "7" {
		t.Fatalf("FncCallOut = %#v, want stringified successful output", result.FncCallOut)
	}
	if result.RawOutput != 7 {
		t.Fatalf("RawOutput = %#v, want original output", result.RawOutput)
	}
}

func TestMakeFunctionCallOutputTimestampsCreatedOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
	tests := []struct {
		name      string
		output    any
		exception error
	}{
		{name: "success", output: "Paris"},
		{name: "tool error", exception: NewToolError("visible failure")},
		{name: "internal error", exception: errors.New("database failure")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, tt.exception)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want created output")
			}
			if result.FncCallOut.CreatedAt.IsZero() {
				t.Fatal("CreatedAt is zero, want generated timestamp")
			}
		})
	}
}

func TestMakeFunctionCallOutputDropsInvalidStructuredOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}
	output := map[string]any{"bad": func() {}}

	result := MakeFunctionCallOutput(call, output, nil)

	if result.FncCallOut != nil {
		t.Fatalf("FncCallOut = %#v, want nil for invalid structured output", result.FncCallOut)
	}
	if result.RawOutput == nil {
		t.Fatal("RawOutput = nil, want original invalid output retained")
	}
	if result.RawError != nil {
		t.Fatalf("RawError = %v, want nil", result.RawError)
	}
}

func TestExecuteFunctionCallReturnsUnknownFunctionOutput(t *testing.T) {
	toolCtx := EmptyToolContext()

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:   "missing",
		CallID: "call_missing",
	}, toolCtx)

	if result.FncCall.Name != "missing" || result.FncCall.CallID != "call_missing" || result.FncCall.Arguments != "{}" {
		t.Fatalf("FncCall = %#v, want defaulted missing call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want unknown function output")
	}
	if !result.FncCallOut.IsError || result.FncCallOut.Output != "Unknown function: missing" {
		t.Fatalf("FncCallOut = %#v, want unknown function error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want unknown function error")
	}
}

func TestExecuteFunctionCallDefaultsEmptyArgumentsAndReturnsOutput(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:   "lookup",
		CallID: "call_lookup",
	}, toolCtx)

	if tool.args != "{}" {
		t.Fatalf("tool args = %q, want default JSON object", tool.args)
	}
	if result.FncCall.Arguments != "{}" {
		t.Fatalf("FncCall.Arguments = %q, want default JSON object", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want successful Paris output", result.FncCallOut)
	}
	if result.RawOutput != "Paris" {
		t.Fatalf("RawOutput = %#v, want Paris", result.RawOutput)
	}
}

func TestExecuteFunctionCallNormalizesToolError(t *testing.T) {
	tool := &recordingTool{name: "lookup", err: NewToolError("visible failure")}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{"city":"Paris"}`,
	}, toolCtx)

	if tool.args != `{"city":"Paris"}` {
		t.Fatalf("tool args = %q, want original arguments", tool.args)
	}
	if result.FncCallOut == nil || !result.FncCallOut.IsError || result.FncCallOut.Output != "visible failure" {
		t.Fatalf("FncCallOut = %#v, want visible tool error", result.FncCallOut)
	}
	if result.RawError == nil {
		t.Fatal("RawError = nil, want tool error")
	}
}

func TestCollectStreamAggregatesChunks(t *testing.T) {
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{
			ID: "req-1",
			Delta: &ChoiceDelta{
				Content: " hello",
				Extra:   map[string]any{"reasoning": "first"},
			},
		}},
		{chunk: &ChatChunk{
			ID: "req-1",
			Delta: &ChoiceDelta{
				Content: " world ",
				ToolCalls: []FunctionToolCall{{
					Type:      "function",
					Name:      "lookup",
					Arguments: `{"city":"Paris"}`,
					CallID:    "call_lookup",
				}},
				Extra: map[string]any{"reasoning": "latest", "trace": "abc"},
			},
		}},
		{chunk: &ChatChunk{
			ID: "req-1",
			Usage: &CompletionUsage{
				CompletionTokens: 3,
				PromptTokens:     5,
				TotalTokens:      8,
			},
		}},
	}}

	collected, err := CollectStream(stream)
	if err != nil {
		t.Fatalf("CollectStream() error = %v", err)
	}
	if collected.Text != "hello world" {
		t.Fatalf("Text = %q, want trimmed aggregate", collected.Text)
	}
	if len(collected.ToolCalls) != 1 || collected.ToolCalls[0].Name != "lookup" {
		t.Fatalf("ToolCalls = %#v, want lookup call", collected.ToolCalls)
	}
	if collected.Usage == nil || collected.Usage.TotalTokens != 8 {
		t.Fatalf("Usage = %#v, want final usage", collected.Usage)
	}
	if collected.Extra["reasoning"] != "latest" || collected.Extra["trace"] != "abc" {
		t.Fatalf("Extra = %#v, want merged latest extra", collected.Extra)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}

func TestCollectStreamClosesAndReturnsStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	stream := &fakeCollectStream{events: []fakeCollectEvent{{err: streamErr}}}

	collected, err := CollectStream(stream)

	if !errors.Is(err, streamErr) {
		t.Fatalf("CollectStream() error = %v, want stream failure", err)
	}
	if collected != nil {
		t.Fatalf("CollectStream() response = %#v, want nil on error", collected)
	}
	if !stream.closed {
		t.Fatal("stream was not closed after error")
	}
}

func TestCollectStreamRejectsNilStream(t *testing.T) {
	collected, err := CollectStream(nil)

	if err == nil {
		t.Fatal("CollectStream(nil) error = nil, want error")
	}
	if collected != nil {
		t.Fatalf("CollectStream(nil) response = %#v, want nil", collected)
	}
}

type recordingTool struct {
	name   string
	args   string
	result string
	err    error
}

func (t *recordingTool) ID() string { return t.name }

func (t *recordingTool) Name() string { return t.name }

func (t *recordingTool) Description() string { return "" }

func (t *recordingTool) Parameters() map[string]any { return nil }

func (t *recordingTool) Execute(_ context.Context, args string) (string, error) {
	t.args = args
	return t.result, t.err
}

type fakeCollectEvent struct {
	chunk *ChatChunk
	err   error
}

type fakeCollectStream struct {
	events []fakeCollectEvent
	closed bool
}

func (s *fakeCollectStream) Next() (*ChatChunk, error) {
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	if event.err != nil {
		return nil, event.err
	}
	return event.chunk, nil
}

func (s *fakeCollectStream) Close() error {
	s.closed = true
	return nil
}
