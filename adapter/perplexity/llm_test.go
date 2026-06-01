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
