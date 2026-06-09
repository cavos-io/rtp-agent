package fireworksai

import "testing"

func TestNewFireworksLLMDefaultsToReferenceModel(t *testing.T) {
	provider := NewFireworksLLM("test-key", "")

	if provider.Model() != "accounts/fireworks/models/llama-v3p3-70b-instruct" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
}

func TestNewFireworksLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "env-key")

	if got := resolveFireworksLLMAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveFireworksLLMAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}
