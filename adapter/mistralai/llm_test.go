package mistralai

import (
	"context"
	"io"
	"net/http"
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

func TestMistralLLMUpdateOptionsAppliesReferenceSamplingParams(t *testing.T) {
	capture := &mistralLLMCaptureHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	provider := NewMistralLLM("test-key", "", withMistralLLMHTTPClient(capture))
	provider.UpdateOptions(
		WithMistralLLMTemperature(0.3),
		WithMistralLLMTopP(0.4),
		WithMistralLLMMaxCompletionTokens(256),
	)

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	for _, want := range []string{`"temperature":0.3`, `"top_p":0.4`, `"max_tokens":256`} {
		if !strings.Contains(capture.requestBody, want) {
			t.Fatalf("request body = %s, want %s", capture.requestBody, want)
		}
	}
	if strings.Contains(capture.requestBody, `"max_completion_tokens"`) {
		t.Fatalf("request body = %s, want reference max_tokens key", capture.requestBody)
	}
}

func TestMistralLLMUpdateOptionsAppliesReferenceToolChoice(t *testing.T) {
	capture := &mistralLLMCaptureHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	provider := NewMistralLLM("test-key", "", withMistralLLMHTTPClient(capture))
	provider.UpdateOptions(WithMistralLLMToolChoice("none"))

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"tool_choice":"none"`) {
		t.Fatalf("request body = %s, want tool_choice none", capture.requestBody)
	}
}

type mistralLLMCaptureHTTPClient struct {
	statusCode   int
	responseBody string
	requestBody  string
}

func (c *mistralLLMCaptureHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	c.requestBody = string(body)
	status := c.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(c.responseBody)),
		Request:    req,
	}, nil
}
