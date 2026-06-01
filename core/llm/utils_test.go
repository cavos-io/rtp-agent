package llm

import (
	"errors"
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
