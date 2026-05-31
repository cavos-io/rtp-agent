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
