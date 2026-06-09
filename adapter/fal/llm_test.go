package fal

import "testing"

func TestNewFalLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "env-key")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalLLM("", "fal-model")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewFalLLM("explicit-key", "fal-model")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewFalLLMUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalLLM("", "fal-model")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}
