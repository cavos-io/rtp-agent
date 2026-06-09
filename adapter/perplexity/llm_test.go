package perplexity

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

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

func TestPerplexityLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("PERPLEXITY_API_KEY", "")
	provider := NewPerplexityLLM("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	_, err := provider.Chat(ctx, chatCtx)
	if err == nil {
		t.Fatal("Chat returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "PERPLEXITY_API_KEY") {
		t.Fatalf("Chat error = %q, want PERPLEXITY_API_KEY guidance", err)
	}
}
