package llm

import (
	"context"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
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

func TestSerializeImageRejectsUnsupportedMIMETypeWithReferenceError(t *testing.T) {
	_, err := SerializeImage(&ImageContent{
		Image: "data:image/bmp;base64,AA==",
	})
	if err == nil {
		t.Fatal("SerializeImage() error = nil, want unsupported mime_type error")
	}

	want := "Unsupported mime_type image/bmp. Must be jpeg, png, webp, or gif"
	if err.Error() != want {
		t.Fatalf("SerializeImage() error = %q, want %q", err, want)
	}
}

func TestSerializeImageRejectsUnsupportedImageTypeWithReferenceError(t *testing.T) {
	_, err := SerializeImage(&ImageContent{Image: 42})
	if err == nil {
		t.Fatal("SerializeImage() error = nil, want unsupported image type error")
	}

	want := "Unsupported image type"
	if err.Error() != want {
		t.Fatalf("SerializeImage() error = %q, want %q", err, want)
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

func TestParseFunctionArgumentsRejectsNestedNonJSONStringWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`"not json object"`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(nested string) error = nil, want error")
	}

	want := "function arguments decoded to a non-JSON string: not json object"
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(nested string) error = %q, want %q", err.Error(), want)
	}
}

func TestParseFunctionArgumentsRejectsNumericNonObjectWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`3`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(number) error = nil, want error")
	}

	want := "expected dict from function arguments, got int: 3"
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(number) error = %q, want %q", err.Error(), want)
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

func TestParseFunctionArgumentsRejectsNonObjectWithReferenceError(t *testing.T) {
	_, err := ParseFunctionArguments(`["Paris"]`)
	if err == nil {
		t.Fatal("ParseFunctionArguments(array) error = nil, want error")
	}

	want := `expected dict from function arguments, got list: ["Paris"]`
	if err.Error() != want {
		t.Fatalf("ParseFunctionArguments(array) error = %q, want %q", err.Error(), want)
	}
}

func TestParseFunctionArgumentsReportsRawPrefixWhenRepairIsEmpty(t *testing.T) {
	const raw = `<|im_end|>`

	_, err := ParseFunctionArguments(raw)
	if err == nil {
		t.Fatal("ParseFunctionArguments(template token) error = nil, want error")
	}

	if !strings.HasPrefix(err.Error(), "could not parse function arguments as JSON: ") {
		t.Fatalf("ParseFunctionArguments(template token) error = %q, want could-not-parse category", err.Error())
	}
	if !strings.HasSuffix(err.Error(), ": "+raw) {
		t.Fatalf("ParseFunctionArguments(template token) error = %q, want raw argument prefix suffix", err.Error())
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

func TestMakeToolOutputReturnsVisibleOutputAndRawValues(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`}

	result := MakeToolOutput(call, "Paris", nil)

	if result.FncCall.CallID != call.CallID || result.FncCall.Name != call.Name || result.FncCall.Arguments != call.Arguments {
		t.Fatalf("FncCall = %#v, want original call", result.FncCall)
	}
	if result.FncCallOut == nil {
		t.Fatal("FncCallOut = nil, want successful output")
	}
	if result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want visible Paris output", result.FncCallOut)
	}
	if result.RawOutput != "Paris" {
		t.Fatalf("RawOutput = %#v, want original raw output", result.RawOutput)
	}
	if result.RawError != nil {
		t.Fatalf("RawError = %v, want nil", result.RawError)
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

	tests := []struct {
		name   string
		output any
		want   string
	}{
		{name: "integer", output: 7, want: "7"},
		{name: "positive infinity", output: math.Inf(1), want: "inf"},
		{name: "negative infinity", output: math.Inf(-1), want: "-inf"},
		{name: "exponent float", output: 1e20, want: "1e+20"},
		{name: "true", output: true, want: "True"},
		{name: "complex", output: complex(1, 2), want: "(1+2j)"},
		{name: "complex positive infinity", output: complex(math.Inf(1), 2), want: "(inf+2j)"},
		{name: "complex imaginary infinity", output: complex(1, math.Inf(1)), want: "(1+infj)"},
		{name: "complex nan", output: complex(math.NaN(), 2), want: "(nan+2j)"},
		{name: "complex negative zero imaginary", output: complex(1, math.Copysign(0, -1)), want: "(1-0j)"},
		{name: "complex negative zero real", output: complex(math.Copysign(0, -1), 2), want: "(-0+2j)"},
		{name: "list", output: []any{1, "x", true}, want: "[1, 'x', True]"},
		{name: "list floats", output: []any{0.0, math.Copysign(0, -1), 1.0, 1.5}, want: "[0.0, -0.0, 1.0, 1.5]"},
		{name: "list exponent floats", output: []any{1e20, 1e-7, 1e-5}, want: "[1e+20, 1e-07, 1e-05]"},
		{name: "list string newline", output: []any{"line\nnext"}, want: "['line\\nnext']"},
		{name: "list string apostrophe", output: []any{"can't"}, want: `["can't"]`},
		{name: "list string nul", output: []any{"\x00"}, want: `['\x00']`},
		{name: "list string backspace", output: []any{"\b"}, want: `['\x08']`},
		{name: "list string escape", output: []any{"\x1b"}, want: `['\x1b']`},
		{name: "dict", output: map[string]any{"ok": true}, want: "{'ok': True}"},
		{name: "dict float", output: map[string]any{"score": 1.0}, want: "{'score': 1.0}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, nil)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want successful output")
			}
			if result.FncCallOut.IsError || result.FncCallOut.Output != tt.want {
				t.Fatalf("FncCallOut = %#v, want output %q", result.FncCallOut, tt.want)
			}
			if !functionOutputTestEqual(result.RawOutput, tt.output) {
				t.Fatalf("RawOutput = %#v, want original output %#v", result.RawOutput, tt.output)
			}
		})
	}
}

func functionOutputTestEqual(got, want any) bool {
	switch wantValue := want.(type) {
	case complex128:
		gotValue, ok := got.(complex128)
		if !ok {
			return false
		}
		return floatTestEqual(real(gotValue), real(wantValue)) && floatTestEqual(imag(gotValue), imag(wantValue))
	default:
		return reflect.DeepEqual(got, want)
	}
}

func floatTestEqual(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return got == want
}

func TestMakeFunctionCallOutputUsesEmptyStringForFalsyOutputs(t *testing.T) {
	call := FunctionCall{CallID: "call_lookup", Name: "lookup", Arguments: "{}"}

	tests := []struct {
		name   string
		output any
	}{
		{name: "false", output: false},
		{name: "zero int", output: 0},
		{name: "zero float", output: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MakeFunctionCallOutput(call, tt.output, nil)

			if result.FncCallOut == nil {
				t.Fatal("FncCallOut = nil, want successful output")
			}
			if result.FncCallOut.IsError || result.FncCallOut.Output != "" {
				t.Fatalf("FncCallOut = %#v, want empty successful output", result.FncCallOut)
			}
			if result.RawOutput != tt.output {
				t.Fatalf("RawOutput = %#v, want original output %#v", result.RawOutput, tt.output)
			}
		})
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

func TestExecuteFunctionCallRepairsMalformedArgumentsBeforeExecutingTool(t *testing.T) {
	tool := &recordingTool{name: "lookup", result: "Paris"}
	toolCtx := NewToolContext([]interface{}{tool})

	result := ExecuteFunctionCall(context.Background(), &FunctionToolCall{
		Name:      "lookup",
		CallID:    "call_lookup",
		Arguments: `{city:"Paris",limit:3,}`,
	}, toolCtx)

	if tool.args != `{"city":"Paris","limit":3}` {
		t.Fatalf("tool args = %q, want repaired JSON object", tool.args)
	}
	if result.FncCall.Arguments != `{"city":"Paris","limit":3}` {
		t.Fatalf("FncCall.Arguments = %q, want repaired JSON object", result.FncCall.Arguments)
	}
	if result.FncCallOut == nil || result.FncCallOut.IsError || result.FncCallOut.Output != "Paris" {
		t.Fatalf("FncCallOut = %#v, want successful Paris output", result.FncCallOut)
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
				CompletionTokens:    3,
				PromptTokens:        5,
				PromptCachedTokens:  2,
				CacheCreationTokens: 1,
				CacheReadTokens:     4,
				TotalTokens:         8,
				ServiceTier:         "priority",
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
	if collected.Usage.CacheCreationTokens != 1 || collected.Usage.CacheReadTokens != 4 || collected.Usage.ServiceTier != "priority" {
		t.Fatalf("Usage metadata = %#v, want cache counters and service tier", collected.Usage)
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

func TestTextStreamYieldsOnlyTextDeltasAndCloses(t *testing.T) {
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "hello"}}},
		{chunk: &ChatChunk{Delta: &ChoiceDelta{ToolCalls: []FunctionToolCall{{Name: "lookup"}}}}},
		{chunk: &ChatChunk{Usage: &CompletionUsage{TotalTokens: 2}}},
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: " world"}}},
	}}
	textStream, err := NewTextStream(stream)
	if err != nil {
		t.Fatalf("NewTextStream() error = %v", err)
	}

	first, err := textStream.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first != "hello" {
		t.Fatalf("first text = %q, want hello", first)
	}
	second, err := textStream.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if second != " world" {
		t.Fatalf("second text = %q, want world delta", second)
	}
	if _, err := textStream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next() error = %v, want EOF", err)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}

func TestTextStreamClosesAndReturnsStreamError(t *testing.T) {
	streamErr := errors.New("stream failed")
	stream := &fakeCollectStream{events: []fakeCollectEvent{
		{chunk: &ChatChunk{Delta: &ChoiceDelta{Content: "hello"}}},
		{err: streamErr},
	}}
	textStream, err := NewTextStream(stream)
	if err != nil {
		t.Fatalf("NewTextStream() error = %v", err)
	}

	if text, err := textStream.Next(); err != nil || text != "hello" {
		t.Fatalf("first Next() = (%q, %v), want hello nil", text, err)
	}
	if _, err := textStream.Next(); !errors.Is(err, streamErr) {
		t.Fatalf("second Next() error = %v, want stream failure", err)
	}
	if !stream.closed {
		t.Fatal("stream was not closed after error")
	}
}

func TestNewTextStreamRejectsNilStream(t *testing.T) {
	textStream, err := NewTextStream(nil)

	if err == nil {
		t.Fatal("NewTextStream(nil) error = nil, want error")
	}
	if textStream != nil {
		t.Fatalf("NewTextStream(nil) stream = %#v, want nil", textStream)
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
