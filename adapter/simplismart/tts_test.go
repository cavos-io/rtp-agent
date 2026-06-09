package simplismart

import "testing"

func TestNewSimplismartTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "env-key")

	provider := NewSimplismartTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSimplismartTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}
