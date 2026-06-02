package tools

import (
	"reflect"
	"testing"
)

func TestEndCallToolParametersUseStrictEmptyObjectSchema(t *testing.T) {
	tool := NewEndCallTool(nil, EndCallToolOptions{})

	params := tool.Parameters()

	want := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
		"required":             []string{},
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Parameters() = %#v, want strict empty object schema", params)
	}
}
