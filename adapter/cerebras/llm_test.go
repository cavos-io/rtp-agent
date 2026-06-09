package cerebras

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

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

func TestCerebrasLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "")
	provider := NewCerebrasLLM("", "")
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
	if !strings.Contains(err.Error(), "CEREBRAS_API_KEY") {
		t.Fatalf("Chat error = %q, want CEREBRAS_API_KEY guidance", err)
	}
}
