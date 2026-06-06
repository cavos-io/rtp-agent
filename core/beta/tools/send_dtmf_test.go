package tools

import (
	"context"
	"reflect"
	"strings"
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

func TestSendDTMFToolReturnsFailureOutputForInvalidEvent(t *testing.T) {
	tool := NewSendDTMFTool(&fakeDtmfPublisher{})

	output, err := tool.Execute(context.Background(), `{"events":["X"]}`)

	if err != nil {
		t.Fatalf("Execute() error = %v, want failure output", err)
	}
	if !strings.Contains(output, "Failed to send DTMF event: X. Error:") {
		t.Fatalf("Execute() output = %q, want invalid event failure", output)
	}
}

type fakeDtmfPublisher struct{}

func (fakeDtmfPublisher) PublishDTMF(int32, string) error {
	return nil
}
