package openai

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/adapter/livekit"
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
	t.Setenv(liveKitInferenceURLEnv, "https://livekit.local/v1")

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
	if provider.baseURL != "https://livekit.local/v1" {
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
	if err == nil {
		t.Fatal("NewLiveKitInferenceLLM error = nil, want missing API key error")
	}
	if got, want := err.Error(), "api_key is required, either as argument or set LIVEKIT_API_KEY environmental variable"; got != want {
		t.Fatalf("NewLiveKitInferenceLLM error = %q, want %q", got, want)
	}

	_, err = NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "")
	if err == nil {
		t.Fatal("NewLiveKitInferenceLLM error = nil, want missing API secret error")
	}
	if got, want := err.Error(), "api_secret is required, either as argument or set LIVEKIT_API_SECRET environmental variable"; got != want {
		t.Fatalf("NewLiveKitInferenceLLM error = %q, want %q", got, want)
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
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.HasPrefix(capture.userAgent, "LiveKit Agents/") {
		t.Fatalf("User-Agent = %q, want LiveKit Agents version prefix", capture.userAgent)
	}
	if !strings.Contains(capture.userAgent, " (go ") {
		t.Fatalf("User-Agent = %q, want Go runtime marker", capture.userAgent)
	}
	if got := capture.header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestLiveKitInferenceLLMChatSendsReferenceContextHeaders(t *testing.T) {
	restore := livekit.SetContextHeadersProvider(func() map[string]string {
		return map[string]string{
			livekit.HeaderRoomID: "RM_llm",
			livekit.HeaderJobID:  "job_llm",
		}
	})
	defer restore()

	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if capture.roomID != "RM_llm" {
		t.Fatalf("%s = %q, want RM_llm", livekit.HeaderRoomID, capture.roomID)
	}
	if capture.jobID != "job_llm" {
		t.Fatalf("%s = %q, want job_llm", livekit.HeaderJobID, capture.jobID)
	}
}

func TestLiveKitInferenceLLMChatSendsReferenceRoutingHeaders(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM(
		"google/gemini-2.5-flash",
		"key",
		"secret",
		WithLiveKitInferenceLLMProvider("google"),
		WithLiveKitInferenceLLMClass("standard"),
	)
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{"inference_class": "priority"}),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	if capture.inferenceProvider != "google" {
		t.Fatalf("%s = %q, want google", livekit.HeaderInferenceProvider, capture.inferenceProvider)
	}
	if capture.inferencePriority != "priority" {
		t.Fatalf("%s = %q, want priority", livekit.HeaderInferencePriority, capture.inferencePriority)
	}
	if strings.Contains(capture.requestBody, "inference_class") {
		t.Fatalf("request body = %s, want inference_class consumed as header", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMUpdateOptionsChangesReferenceModel(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	provider.UpdateOptions(WithLiveKitInferenceLLMModel("openai/gpt-5-mini"))

	if provider.Model() != "openai/gpt-5-mini" {
		t.Fatalf("Model = %q, want updated model", provider.Model())
	}

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"model":"openai/gpt-5-mini"`) {
		t.Fatalf("request body = %s, want updated model", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMUpdateOptionsReplacesReferenceExtraParams(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret",
		WithLiveKitInferenceLLMExtraParams(map[string]any{
			"temperature": 0.2,
			"user":        "initial-user",
		}),
	)
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	provider.UpdateOptions(WithLiveKitInferenceLLMExtraParams(map[string]any{
		"top_p": 0.4,
		"user":  "updated-user",
	}))

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if strings.Contains(capture.requestBody, `"temperature"`) {
		t.Fatalf("request body = %s, want replaced extra params without temperature", capture.requestBody)
	}
	if !strings.Contains(capture.requestBody, `"top_p":0.4`) {
		t.Fatalf("request body = %s, want updated top_p", capture.requestBody)
	}
	if !strings.Contains(capture.requestBody, `"user":"updated-user"`) {
		t.Fatalf("request body = %s, want updated user", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMChatOmitsDefaultReasoningEffort(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-5", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if strings.Contains(capture.requestBody, `"reasoning_effort"`) {
		t.Fatalf("request body = %s, want no default reasoning_effort for LiveKit inference LLM", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMChatRequestsReferenceUsageStreamOptions(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret",
		WithLiveKitInferenceLLMExtraParams(map[string]any{
			"stream_options": map[string]any{"include_usage": false},
		}),
	)
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"stream_options":{"include_usage":true}`) {
		t.Fatalf("request body = %s, want stream_options include_usage true", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMChatSendsReferenceExtraHeaders(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret",
		WithLiveKitInferenceLLMExtraParams(map[string]any{
			"extra_headers": map[string]any{
				"X-Trace-ID": "trace-123",
			},
			"user": "caller-123",
		}),
	)
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if got := capture.header.Get("X-Trace-ID"); got != "trace-123" {
		t.Fatalf("X-Trace-ID = %q, want trace-123", got)
	}
	if !strings.Contains(capture.requestBody, `"user":"caller-123"`) {
		t.Fatalf("request body = %s, want non-header extra param preserved", capture.requestBody)
	}
	if strings.Contains(capture.requestBody, "extra_headers") {
		t.Fatalf("request body = %s, want extra_headers consumed as headers", capture.requestBody)
	}
}

func TestLiveKitInferenceLLMChatSendsReferenceCallExtraHeaders(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusUnauthorized,
		responseBody: `{"error":{"message":"stop"}}`,
	}

	provider, err := NewLiveKitInferenceLLM("openai/gpt-4.1", "key", "secret")
	if err != nil {
		t.Fatalf("NewLiveKitInferenceLLM error = %v", err)
	}
	provider.baseURL = "https://livekit.test/v1"
	provider.httpClient = capture

	_, _ = provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{
			"extra_headers": map[string]any{
				"X-Call-ID": "call-123",
			},
			"user": "caller-456",
		}),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	if got := capture.header.Get("X-Call-ID"); got != "call-123" {
		t.Fatalf("X-Call-ID = %q, want call-123", got)
	}
	if !strings.Contains(capture.requestBody, `"user":"caller-456"`) {
		t.Fatalf("request body = %s, want non-header call extra param preserved", capture.requestBody)
	}
	if strings.Contains(capture.requestBody, "extra_headers") {
		t.Fatalf("request body = %s, want call extra_headers consumed as headers", capture.requestBody)
	}
}
