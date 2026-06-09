package cerebras

import "testing"

func TestNewCerebrasLLMDefaultsMatchReference(t *testing.T) {
	provider := NewCerebrasLLM("test-key", "")

	if provider.Model() != "llama3.1-8b" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
	if provider.BaseURL() != "https://api.cerebras.ai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.BaseURL())
	}
}

func TestNewCerebrasLLMUsesCustomModel(t *testing.T) {
	provider := NewCerebrasLLM("test-key", "qwen-3-32b")

	if provider.Model() != "qwen-3-32b" {
		t.Fatalf("model = %q, want custom model", provider.Model())
	}
}

func TestNewCerebrasLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "env-key")

	if got := resolveCerebrasAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveCerebrasAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}
