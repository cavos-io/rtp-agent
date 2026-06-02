package baseten

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestBasetenLLMDefaultsMatchReference(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "env-key")

	provider, err := NewBasetenLLM("", "")
	if err != nil {
		t.Fatalf("NewBasetenLLM error = %v, want nil from env key", err)
	}

	if provider.Model() != "meta-llama/Llama-4-Maverick-17B-128E-Instruct" {
		t.Fatalf("Model = %q, want reference default", provider.Model())
	}
	if provider.Provider() != "Baseten" {
		t.Fatalf("Provider = %q, want Baseten", provider.Provider())
	}
	if provider.baseURL != "https://inference.baseten.co/v1" {
		t.Fatalf("baseURL = %q, want reference inference endpoint", provider.baseURL)
	}
}

func TestBasetenLLMRequiresAPIKey(t *testing.T) {
	t.Setenv(basetenAPIKeyEnv, "")

	_, err := NewBasetenLLM("", "")

	if err == nil || !strings.Contains(err.Error(), "BASETEN_API_KEY") {
		t.Fatalf("NewBasetenLLM error = %v, want API key error", err)
	}
}

func TestBasetenLLMChatDelegatesToOpenAICompatibleEndpoint(t *testing.T) {
	var gotAuth string
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		http.Error(w, `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	provider, err := newBasetenLLMWithBaseURL("test-key", "custom-model", server.URL+"/v1")
	if err != nil {
		t.Fatalf("newBasetenLLMWithBaseURL error = %v", err)
	}

	_, err = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError from local endpoint", err, err)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", gotAuth)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path = %q, want OpenAI-compatible chat completions path", gotPath)
	}
}
