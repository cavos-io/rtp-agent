package telnyx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
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
	if got := llm.Provider(provider); got != "api.telnyx.com" {
		t.Fatalf("provider metadata = %q, want reference base URL host", got)
	}
}

func TestNewTelnyxLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	provider := NewTelnyxLLM("", "")
	if provider.err != nil {
		t.Fatalf("NewTelnyxLLM error = %v, want env key to satisfy constructor", provider.err)
	}
	explicit := NewTelnyxLLM("explicit-key", "")
	if explicit.err != nil {
		t.Fatalf("NewTelnyxLLM explicit error = %v, want explicit key to satisfy constructor", explicit.err)
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

func TestTelnyxLLMForwardsReferenceConstructorOptions(t *testing.T) {
	capture := &captureTelnyxLLMHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request"}}`,
	}
	provider := NewTelnyxLLM("test-key", "telnyx-chat",
		WithTelnyxLLMBaseURL("https://telnyx.example/v2/ai"),
		WithTelnyxLLMHTTPClient(capture),
		WithTelnyxLLMOptions(
			adapteropenai.WithOpenAILLMUser("caller-1"),
			adapteropenai.WithOpenAILLMTemperature(0.4),
			adapteropenai.WithOpenAILLMTopP(0.7),
			adapteropenai.WithOpenAILLMParallelToolCalls(false),
			adapteropenai.WithOpenAILLMToolChoice("required"),
		),
	)
	chatCtx := llm.NewChatContext()
	chatCtx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	if got := llm.Provider(provider); got != "telnyx.example" {
		t.Fatalf("provider metadata = %q, want configured base URL host", got)
	}

	stream, err := provider.Chat(context.Background(), chatCtx,
		llm.WithTools([]llm.Tool{telnyxLLMTestTool{}}),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v", err)
	}
	_, _ = stream.Next()

	if !strings.Contains(capture.requestURL, "https://telnyx.example/v2/ai/chat/completions") {
		t.Fatalf("request URL = %q, want configured Telnyx base URL", capture.requestURL)
	}
	for _, want := range []string{
		`"user":"caller-1"`,
		`"temperature":0.4`,
		`"top_p":0.7`,
		`"parallel_tool_calls":false`,
		`"tool_choice":"required"`,
	} {
		if !strings.Contains(capture.requestBody, want) {
			t.Fatalf("request body = %s, missing %s", capture.requestBody, want)
		}
	}
}

func TestTelnyxLLMCloseForwardsToOpenAIProvider(t *testing.T) {
	capture := &captureTelnyxLLMHTTPClient{
		statusCode:   http.StatusOK,
		responseBody: "data: [DONE]\n\n",
	}
	provider := NewTelnyxLLM("test-key", "telnyx-chat", WithTelnyxLLMHTTPClient(capture))

	if err := llm.Close(provider); err != nil {
		t.Fatalf("llm.Close error = %v", err)
	}
	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if stream != nil {
		t.Fatalf("Chat stream after Close = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Chat after Close error = %T %v, want io.ErrClosedPipe", err, err)
	}
	if capture.requestURL != "" {
		t.Fatalf("request URL after Close = %q, want no HTTP request", capture.requestURL)
	}
}

type telnyxLLMTestTool struct{}

func (telnyxLLMTestTool) ID() string          { return "lookup" }
func (telnyxLLMTestTool) Name() string        { return "lookup" }
func (telnyxLLMTestTool) Description() string { return "look up information" }
func (telnyxLLMTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (telnyxLLMTestTool) Execute(context.Context, string) (string, error) { return "", nil }

type captureTelnyxLLMHTTPClient struct {
	requestURL   string
	requestBody  string
	statusCode   int
	responseBody string
}

func (c *captureTelnyxLLMHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.requestURL = req.URL.String()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	c.requestBody = string(body)
	statusCode := c.statusCode
	if statusCode == 0 {
		statusCode = http.StatusBadRequest
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(c.responseBody)),
		Request:    req,
	}, nil
}
