package simplismart

import (
	"context"
	"strings"
	"testing"
)

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

func TestSimplismartTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "")
	provider := NewSimplismartTTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Synthesize error = %q, want SIMPLISMART_API_KEY guidance", err)
	}
}
