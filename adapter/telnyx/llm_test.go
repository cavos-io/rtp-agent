package telnyx

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewTelnyxLLMDefaultsMatchReference(t *testing.T) {
	provider := NewTelnyxLLM("test-key", "")

	if provider.Model() != "meta-llama/Meta-Llama-3.1-70B-Instruct" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
	if provider.BaseURL() != "https://api.telnyx.com/v2/ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.BaseURL())
	}
}

func TestNewTelnyxLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	if got := resolveTelnyxLLMAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveTelnyxLLMAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}

func TestTelnyxLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "")
	provider := NewTelnyxLLM("", "")
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
	if !strings.Contains(err.Error(), "TELNYX_API_KEY") {
		t.Fatalf("Chat error = %q, want TELNYX_API_KEY guidance", err)
	}
}
