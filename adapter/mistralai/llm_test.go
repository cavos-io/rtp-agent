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

func TestNewMistralLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key")

	if got := resolveMistralLLMAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveMistralLLMAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}
