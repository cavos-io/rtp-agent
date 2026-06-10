package groq

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewGroqLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "env-key")

	if got := resolveGroqAPIKey(""); got != "env-key" {
		t.Fatalf("resolved API key = %q, want env key", got)
	}
	if got := resolveGroqAPIKey("explicit-key"); got != "explicit-key" {
		t.Fatalf("resolved API key = %q, want explicit key", got)
	}
}

func TestNewGroqLLMDefaultsMatchReference(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "env-key")

	provider := NewGroqLLM("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if provider.Model() != "llama-3.3-70b-versatile" {
		t.Fatalf("model = %q, want reference default model", provider.Model())
	}
	if provider.baseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.Provider() != "groq" {
		t.Fatalf("provider = %q, want groq", provider.Provider())
	}
}

func TestNewGroqLLMOptionsMatchReference(t *testing.T) {
	provider := NewGroqLLM("test-key", "openai/gpt-oss-20b",
		WithGroqLLMBaseURL("https://groq.example/openai/v1/"),
	)

	if provider.apiKey != "test-key" {
		t.Fatalf("api key = %q, want explicit key", provider.apiKey)
	}
	if provider.Model() != "openai/gpt-oss-20b" {
		t.Fatalf("model = %q, want configured model", provider.Model())
	}
	if provider.baseURL != "https://groq.example/openai/v1" {
		t.Fatalf("base URL = %q, want trimmed configured base URL", provider.baseURL)
	}
	if provider.reasoningEffort != "low" {
		t.Fatalf("reasoning effort = %q, want reference default low", provider.reasoningEffort)
	}
}

func TestNewGroqLLMReasoningEffortDefaultsMatchReference(t *testing.T) {
	if got := NewGroqLLM("test-key", "openai/gpt-oss-120b").reasoningEffort; got != "low" {
		t.Fatalf("gpt-oss reasoning effort = %q, want low", got)
	}
	if got := NewGroqLLM("test-key", "qwen/qwen3-32b").reasoningEffort; got != "none" {
		t.Fatalf("qwen reasoning effort = %q, want none", got)
	}
	if got := NewGroqLLM("test-key", "moonshotai/kimi-k2-instruct-0905").reasoningEffort; got != "" {
		t.Fatalf("kimi reasoning effort = %q, want unset", got)
	}
	if got := NewGroqLLM("test-key", "openai/gpt-oss-20b", WithGroqLLMReasoningEffort("medium")).reasoningEffort; got != "medium" {
		t.Fatalf("explicit reasoning effort = %q, want medium", got)
	}
}

func TestGroqLLMRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")
	provider := NewGroqLLM("", "")
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
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Fatalf("Chat error = %q, want GROQ_API_KEY guidance", err)
	}
}
