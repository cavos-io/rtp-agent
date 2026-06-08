package openai

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewLiveKitInferenceLLMUsesEnvironmentCredentialsAndReferenceDefaults(t *testing.T) {
	t.Setenv(liveKitInferenceAPIKeyEnv, "inference-key")
	t.Setenv(liveKitInferenceAPISecretEnv, "inference-secret")
	t.Setenv(liveKitAPIKeyEnv, "livekit-key")
	t.Setenv(liveKitAPISecretEnv, "livekit-secret")
	t.Setenv(liveKitInferenceURLEnv, "")

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "", "")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v, want env fallback", err)
	}

	if provider.apiKey != "inference-key" {
		t.Fatalf("apiKey = %q, want inference env key", provider.apiKey)
	}
	if provider.apiSecret != "inference-secret" {
		t.Fatalf("apiSecret = %q, want inference env secret", provider.apiSecret)
	}
	if provider.baseURL != defaultLiveKitInferenceLLMURL {
		t.Fatalf("baseURL = %q, want default gateway", provider.baseURL)
	}
	if provider.Model() != "openai/gpt-4.1" {
		t.Fatalf("Model = %q, want configured model", provider.Model())
	}
	if provider.Provider() != "livekit" {
		t.Fatalf("Provider = %q, want livekit", provider.Provider())
	}
}

func TestNewLiveKitInferenceLLMFallsBackToLiveKitCredentialsAndURL(t *testing.T) {
	t.Setenv(liveKitInferenceAPIKeyEnv, "")
	t.Setenv(liveKitInferenceAPISecretEnv, "")
	t.Setenv(liveKitAPIKeyEnv, "livekit-key")
	t.Setenv(liveKitAPISecretEnv, "livekit-secret")
	t.Setenv(liveKitInferenceURLEnv, "https://inference.local/v1")

	provider, err := NewLiveKitInferenceLLM("google/gemini-2.5-flash", "", "")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v, want LiveKit env fallback", err)
	}

	if provider.apiKey != "livekit-key" {
		t.Fatalf("apiKey = %q, want LiveKit API key fallback", provider.apiKey)
	}
	if provider.apiSecret != "livekit-secret" {
		t.Fatalf("apiSecret = %q, want LiveKit API secret fallback", provider.apiSecret)
	}
	if provider.baseURL != "https://inference.local/v1" {
		t.Fatalf("baseURL = %q, want inference URL env", provider.baseURL)
	}
}

func TestNewLiveKitInferenceLLMRequiresModelAndCredentials(t *testing.T) {
	t.Setenv(liveKitInferenceAPIKeyEnv, "")
	t.Setenv(liveKitInferenceAPISecretEnv, "")
	t.Setenv(liveKitAPIKeyEnv, "")
	t.Setenv(liveKitAPISecretEnv, "")

	_, err := NewLiveKitInferenceLLM("", "key", "secret")
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("NewLiveKitInferenceLLM error = %v, want missing model error", err)
	}

	_, err = NewLiveKitInferenceLLM("openai/gpt-4.1", "", "secret")
	if err == nil || !strings.Contains(err.Error(), "LIVEKIT_API_KEY") {
		t.Fatalf("NewLiveKitInferenceLLM error = %v, want missing API key error", err)
	}

	_, err = NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "")
	if err == nil || !strings.Contains(err.Error(), "LIVEKIT_API_SECRET") {
		t.Fatalf("NewLiveKitInferenceLLM error = %v, want missing API secret error", err)
	}
}

func TestLiveKitInferenceLLMChatBuildsTokenBeforeDelegating(t *testing.T) {
	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = provider.Chat(ctx, llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))
	if err == nil {
		t.Fatal("Chat error = nil, want canceled request error after token creation")
	}
}

func TestLiveKitInferenceLLMChatSendsReferenceInferenceHeaders(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://inference.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.HasPrefix(capture.userAgent, "LiveKit Agents/") {
		t.Fatalf("User-Agent = %q, want LiveKit Agents version prefix", capture.userAgent)
	}
	if !strings.Contains(capture.userAgent, " (go ") {
		t.Fatalf("User-Agent = %q, want Go runtime marker", capture.userAgent)
	}
}
