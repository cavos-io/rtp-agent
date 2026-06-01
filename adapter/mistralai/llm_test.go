package mistralai

import "testing"

func TestNewMistralLLMDefaultsToReferenceModel(t *testing.T) {
	provider := NewMistralLLM("test-key", "")

	if provider.Model() != "ministral-8b-latest" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
}

func TestNewMistralLLMUsesCustomModel(t *testing.T) {
	provider := NewMistralLLM("test-key", "mistral-small-latest")

	if provider.Model() != "mistral-small-latest" {
		t.Fatalf("model = %q, want custom model", provider.Model())
	}
}
