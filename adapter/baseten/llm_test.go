package baseten

import (
	"context"
	"errors"
	"io"
	"net/http"
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
	client := &captureBasetenHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}

	provider, err := newBasetenLLMWithBaseURLAndHTTPClient("test-key", "custom-model", "https://baseten.test/v1", client)
	if err != nil {
		t.Fatalf("newBasetenLLMWithBaseURL error = %v", err)
	}

	_, err = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError from local endpoint", err, err)
	}
	if client.authorization != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", client.authorization)
	}
	if client.path != "/v1/chat/completions" {
		t.Fatalf("path = %q, want OpenAI-compatible chat completions path", client.path)
	}
}

type captureBasetenHTTPClient struct {
	statusCode    int
	responseBody  string
	authorization string
	path          string
}

func (c *captureBasetenHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.authorization = req.Header.Get("Authorization")
	c.path = req.URL.Path
	statusCode := c.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(strings.NewReader(c.responseBody)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
