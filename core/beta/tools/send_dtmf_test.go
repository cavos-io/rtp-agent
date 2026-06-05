package tools

import (
	"reflect"
	"testing"
)

func TestSendDTMFToolParametersUseStrictObjectSchema(t *testing.T) {
	tool := NewSendDTMFTool(nil)

	params := tool.Parameters()

	want := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"events": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "*", "#", "A", "B", "C", "D"},
				},
			},
		},
		"required": []string{"events"},
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Parameters() = %#v, want strict DTMF events schema", params)
	}
}
