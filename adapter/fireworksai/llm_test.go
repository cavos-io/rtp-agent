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
	if provider.Provider() != "fireworks" {
		t.Fatalf("provider = %q, want fireworks adapter label", provider.Provider())
	}
}

func TestNewFireworksLLMUsesReferenceEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "env-key")

	provider := NewFireworksLLM("", "accounts/fireworks/models/custom")

	if provider.err != nil {
		t.Fatalf("constructor error = %v, want env key accepted", provider.err)
	}
	if provider.Model() != "accounts/fireworks/models/custom" {
		t.Fatalf("model = %q, want custom model", provider.Model())
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
