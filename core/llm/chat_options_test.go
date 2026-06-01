package llm

import "testing"

func TestWithExtraParamsStoresClone(t *testing.T) {
	params := map[string]any{
		"temperature": 0.7,
	}
	options := &ChatOptions{}

	WithExtraParams(params)(options)
	params["temperature"] = 1.0

	if options.ExtraParams["temperature"] != 0.7 {
		t.Fatalf("ExtraParams[temperature] = %v, want 0.7", options.ExtraParams["temperature"])
	}
}

func TestWithResponseFormatStoresClone(t *testing.T) {
	format := map[string]any{
		"type": "json_object",
	}
	options := &ChatOptions{}

	WithResponseFormat(format)(options)
	format["type"] = "text"

	if options.ResponseFormat["type"] != "json_object" {
		t.Fatalf("ResponseFormat[type] = %v, want json_object", options.ResponseFormat["type"])
	}
}
