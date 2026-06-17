package mistralai

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewMistralLLMDefaultsToReferenceModel(t *testing.T) {
	provider := NewMistralLLM("test-key", "")

	if provider.Model() != "ministral-8b-latest" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
}

func TestNewMistralLLMUsesCustomModel(t *testing.T) {
	provider := NewMistralLLM("test-key", "mistral-small-latest")

	if provider.Model() != "mistral-small-latest" {
		t.Fatalf("model = %q, want custom model", provider.Model())
	}
}

func TestNewMistralLLMExposesReferenceProviderMetadata(t *testing.T) {
	provider := NewMistralLLM("test-key", "")

	if got := llm.Provider(provider); got != "MistralAI" {
		t.Fatalf("provider metadata = %q, want MistralAI", got)
	}
}

func TestNewMistralLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key")

	if got := resolveMistralLLMAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveMistralLLMAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}

func TestMistralLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	provider := NewMistralLLM("", "")
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
	if !strings.Contains(err.Error(), "MISTRAL_API_KEY") {
		t.Fatalf("Chat error = %q, want MISTRAL_API_KEY guidance", err)
	}
}

func TestMistralLLMUpdateOptionsChangesReferenceModel(t *testing.T) {
	provider := NewMistralLLM("test-key", "")

	provider.UpdateOptions(WithMistralLLMModel("mistral-small-latest"))

	if provider.Model() != "mistral-small-latest" {
		t.Fatalf("model = %q, want updated model", provider.Model())
	}
}
