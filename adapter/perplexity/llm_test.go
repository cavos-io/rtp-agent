package perplexity

import "testing"

func TestNewPerplexityLLMDefaultsMatchReference(t *testing.T) {
	provider := NewPerplexityLLM("test-key", "")

	if provider.Model() != "sonar-pro" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
	if provider.BaseURL() != "https://api.perplexity.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.BaseURL())
	}
}

func TestNewPerplexityLLMUsesCustomModel(t *testing.T) {
	provider := NewPerplexityLLM("test-key", "sonar")

	if provider.Model() != "sonar" {
		t.Fatalf("model = %q, want custom model", provider.Model())
	}
}

func TestNewPerplexityLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("PERPLEXITY_API_KEY", "env-key")

	if got := resolvePerplexityAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolvePerplexityAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}
