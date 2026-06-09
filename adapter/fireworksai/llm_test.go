package fireworksai

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

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

func TestFireworksLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "")
	provider := NewFireworksLLM("", "")
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
	if !strings.Contains(err.Error(), "FIREWORKS_API_KEY") {
		t.Fatalf("Chat error = %q, want FIREWORKS_API_KEY guidance", err)
	}
}
