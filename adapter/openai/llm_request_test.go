package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

func mustNewOpenAILLMWithConfig(t *testing.T, config openaisdk.ClientConfig, model string) *OpenAILLM {
	t.Helper()
	provider, err := newOpenAILLMWithConfigAndModel(config, model)
	if err != nil {
		t.Fatalf("newOpenAILLMWithConfigAndModel error = %v", err)
	}
	return provider
}

type requestTestTool struct{}

func (requestTestTool) ID() string          { return "lookup" }
func (requestTestTool) Name() string        { return "lookup" }
func (requestTestTool) Description() string { return "look up information" }
func (requestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (requestTestTool) Execute(context.Context, string) (string, error) { return "", nil }

func TestNewOpenAILLMUsesEnvironmentAPIKeyAndReferenceDefaultModel(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")

	model, err := NewOpenAILLM("", "")
	if err != nil {
		t.Fatalf("NewOpenAILLM error = %v, want env fallback", err)
	}

	if model.Model() != defaultOpenAILLMModel {
		t.Fatalf("Model = %q, want reference default", model.Model())
	}
}

func TestNewOpenAILLMRequiresAPIKey(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "")

	_, err := NewOpenAILLM("", "")
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("NewOpenAILLM error = %v, want missing API key error", err)
	}
}

func TestNewOpenAILLMChatUsesConfiguredKeyAndDefaultModel(t *testing.T) {
	t.Setenv(openAIAPIKeyEnv, "env-key")
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("env-key")
	config.HTTPClient = capture

	model, err := newOpenAILLMWithConfigAndModel(config, "")
	if err != nil {
		t.Fatalf("newOpenAILLMWithConfigAndModel error = %v", err)
	}

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if capture.requestBody == "" || !strings.Contains(capture.requestBody, `"model":"gpt-4.1"`) {
		t.Fatalf("request body = %s, want default model", capture.requestBody)
	}
	if capture.authorization != "Bearer env-key" {
		t.Fatalf("Authorization = %q, want bearer env key", capture.authorization)
	}
}

func TestOpenAIChatOmitsDefaultParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if strings.Contains(capture.requestBody, `"parallel_tool_calls"`) {
		t.Fatalf("request body = %s, want default parallel_tool_calls omitted", capture.requestBody)
	}
}

func TestOpenAIChatSerializesExplicitParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, _ = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithParallelToolCalls(false),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	if !strings.Contains(capture.requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want explicit parallel_tool_calls false", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderParallelToolCalls(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMParallelToolCalls(false),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want provider parallel_tool_calls false", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderToolChoice(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMToolChoice("none"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"tool_choice":"none"`) {
		t.Fatalf("request body = %s, want provider tool_choice none", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderTemperature(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMTemperature(0.3),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"temperature":0.3`) {
		t.Fatalf("request body = %s, want provider temperature", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderTopP(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMTopP(0.4),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"top_p":0.4`) {
		t.Fatalf("request body = %s, want provider top_p", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderMaxCompletionTokens(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMMaxCompletionTokens(256),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"max_completion_tokens":256`) {
		t.Fatalf("request body = %s, want provider max_completion_tokens", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderStore(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMStore(true),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"store":true`) {
		t.Fatalf("request body = %s, want provider store true", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderServiceTier(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMServiceTier("priority"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"service_tier":"priority"`) {
		t.Fatalf("request body = %s, want provider service_tier", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderSafetyIdentifier(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMSafetyIdentifier("hashed-user"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"safety_identifier":"hashed-user"`) {
		t.Fatalf("request body = %s, want provider safety_identifier", capture.requestBody)
	}
}

func TestOpenAIChatAppliesProviderUser(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient(
		"test-key",
		"gpt-4o",
		"https://openai.test/v1",
		capture,
		WithOpenAILLMUser("caller-123"),
	)

	_, _ = model.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(capture.requestBody, `"user":"caller-123"`) {
		t.Fatalf("request body = %s, want provider user", capture.requestBody)
	}
}

func TestOpenAIChatAppliesConnectOptionsTimeoutToRequestContext(t *testing.T) {
	sentinelErr := errors.New("stop after context capture")
	capture := &captureDeadlineHTTPClient{err: sentinelErr}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0, Timeout: 75 * time.Millisecond}),
	)

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	if !capture.hasDeadline {
		t.Fatal("request context has no deadline, want connect options timeout deadline")
	}
	if capture.remaining <= 0 || capture.remaining > 75*time.Millisecond {
		t.Fatalf("request context deadline remaining = %v, want bounded by connect timeout", capture.remaining)
	}
}

func TestOpenAIChatAppliesDefaultConnectOptionsTimeoutToRequestContext(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(context.Background(), llm.NewChatContext())

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if !capture.hasDeadline {
		t.Fatal("request context has no deadline, want default connect timeout deadline")
	}
	if capture.remaining <= 0 || capture.remaining > llm.DefaultAPIConnectOptions().Timeout {
		t.Fatalf("request context deadline remaining = %v, want bounded by default connect timeout", capture.remaining)
	}
}

func TestBuildOpenAIChatCompletionRequestAppliesExtraParams(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ParallelToolCalls: true,
		ExtraParams: map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"max_completion_tokens": 128,
			"logit_bias":            map[string]any{"42": 7.0},
			"reasoning_effort":      "low",
			"metadata":              map[string]any{"trace": "abc"},
			"seed":                  42,
			"stop":                  []string{"END"},
			"user":                  "caller-123",
			"store":                 true,
			"stream_options":        map[string]any{"include_usage": true},
			"service_tier":          "priority",
			"verbosity":             "low",
			"safety_identifier":     "hashed-user",
			"chat_template_kwargs":  map[string]any{"enable_thinking": false},
			"prediction":            map[string]any{"type": "content", "content": "known prefix"},
		},
	})

	if req.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", req.Temperature)
	}
	if req.TopP != 0.8 {
		t.Fatalf("TopP = %v, want 0.8", req.TopP)
	}
	if req.PresencePenalty != 0.1 {
		t.Fatalf("PresencePenalty = %v, want 0.1", req.PresencePenalty)
	}
	if req.FrequencyPenalty != 0.2 {
		t.Fatalf("FrequencyPenalty = %v, want 0.2", req.FrequencyPenalty)
	}
	if req.N != 2 {
		t.Fatalf("N = %d, want 2", req.N)
	}
	if req.MaxCompletionTokens != 128 {
		t.Fatalf("MaxCompletionTokens = %d, want 128", req.MaxCompletionTokens)
	}
	if req.LogitBias["42"] != 7 {
		t.Fatalf("LogitBias = %#v, want token 42 bias 7", req.LogitBias)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want low", req.ReasoningEffort)
	}
	if req.Metadata["trace"] != "abc" {
		t.Fatalf("Metadata[trace] = %q, want abc", req.Metadata["trace"])
	}
	if req.Seed == nil || *req.Seed != 42 {
		t.Fatalf("Seed = %#v, want 42", req.Seed)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("Stop = %#v, want END", req.Stop)
	}
	if req.User != "caller-123" {
		t.Fatalf("User = %q, want caller-123", req.User)
	}
	if !req.Store {
		t.Fatal("Store = false, want true")
	}
	if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
		t.Fatalf("StreamOptions = %#v, want include_usage", req.StreamOptions)
	}
	if req.ServiceTier != openaisdk.ServiceTierPriority {
		t.Fatalf("ServiceTier = %q, want priority", req.ServiceTier)
	}
	if req.Verbosity != "low" {
		t.Fatalf("Verbosity = %q, want low", req.Verbosity)
	}
	if req.SafetyIdentifier != "hashed-user" {
		t.Fatalf("SafetyIdentifier = %q, want hashed-user", req.SafetyIdentifier)
	}
	if enabled, ok := req.ChatTemplateKwargs["enable_thinking"].(bool); !ok || enabled {
		t.Fatalf("ChatTemplateKwargs = %#v, want enable_thinking false", req.ChatTemplateKwargs)
	}
	if req.Prediction == nil || req.Prediction.Type != "content" || req.Prediction.Content != "known prefix" {
		t.Fatalf("Prediction = %#v, want content prediction", req.Prediction)
	}
}

func TestNewOpenAILLMWithBaseURLAndHTTPClientUsesConfiguredClient(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`,
	}
	model := NewOpenAILLMWithBaseURLAndHTTPClient("test-key", "gpt-4o", "https://openai.test/v1", capture)

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if capture.authorization != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", capture.authorization)
	}
}

type captureDeadlineHTTPClient struct {
	err           error
	hasDeadline   bool
	remaining     time.Duration
	statusCode    int
	responseBody  string
	header        http.Header
	requestBody   string
	authorization string
}

func (c *captureDeadlineHTTPClient) Do(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	c.hasDeadline = ok
	if ok {
		c.remaining = time.Until(deadline)
	}
	c.authorization = req.Header.Get("Authorization")
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		c.requestBody = string(body)
		req.Body = io.NopCloser(strings.NewReader(c.requestBody))
	}
	if c.err != nil {
		return nil, c.err
	}
	statusCode := c.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	header := c.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(strings.NewReader(c.responseBody)),
		Header:     header,
		Request:    req,
	}, nil
}

func TestOpenAIChatReturnsAPIStatusErrorOnHTTPError(t *testing.T) {
	capture := &captureDeadlineHTTPClient{
		statusCode:   http.StatusTooManyRequests,
		responseBody: `{"error":{"message":"rate limit","type":"rate_limit","code":"rate_limit"}}`,
	}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "rate limit (429 Too Many Requests)" {
		t.Fatalf("Message = %q, want formatted rate limit message", statusErr.Message)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", statusErr.StatusCode)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want 429 retryable")
	}
}

func TestOpenAIChatReturnsAPITimeoutErrorOnTransportDeadline(t *testing.T) {
	capture := &captureDeadlineHTTPClient{err: context.DeadlineExceeded}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Chat error = %T %v, want APITimeoutError", err, err)
	}
	if timeoutErr.Message != "Request timed out." {
		t.Fatalf("Message = %q, want default timeout message", timeoutErr.Message)
	}
	if !timeoutErr.Retryable {
		t.Fatal("Retryable = false, want timeout errors retryable")
	}
}

func TestOpenAIChatRetriesRetryableSetupAPIError(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusTooManyRequests, `{"error":{"message":"rate limit","type":"rate_limit","code":"rate_limit"}}`),
		openAITestResponse(http.StatusOK, "data: [DONE]\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v, want retry success", err)
	}
	_ = stream.Close()
	if capture.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial failure plus retry", capture.calls)
	}
}

func TestOpenAIChatDoesNotRetryNonRetryableSetupAPIError(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusBadRequest, `{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`),
		openAITestResponse(http.StatusOK, "data: [DONE]\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	_, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if capture.calls != 1 {
		t.Fatalf("HTTP calls = %d, want no retry for non-retryable status", capture.calls)
	}
}

func TestOpenAIStreamReturnsAPIErrorOnErrorEvent(t *testing.T) {
	capture := &sequenceHTTPClient{responses: []*http.Response{
		openAITestResponse(http.StatusOK, `data: {"error":{"message":"stream failed","type":"server_error","code":"server_error"}}`+"\n\n"),
	}}
	config := openaisdk.DefaultConfig("test-key")
	config.HTTPClient = capture
	model := mustNewOpenAILLMWithConfig(t, config, "gpt-4o")

	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if apiErr.Message != "stream failed" {
		t.Fatalf("Message = %q, want OpenAI stream error message", apiErr.Message)
	}
	body, ok := apiErr.Body.(*openaisdk.APIError)
	if !ok {
		t.Fatalf("Body = %T %#v, want OpenAI APIError body", apiErr.Body, apiErr.Body)
	}
	if body.Type != "server_error" || body.Code != "server_error" {
		t.Fatalf("Body = %#v, want server_error metadata", body)
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want stream API errors retryable")
	}
	var connectionErr *llm.APIConnectionError
	if errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIError not APIConnectionError", err, err)
	}
}

type sequenceHTTPClient struct {
	responses []*http.Response
	calls     int
}

func (c *sequenceHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c.calls >= len(c.responses) {
		return nil, errors.New("unexpected HTTP call")
	}
	resp := c.responses[c.calls]
	resp.Request = req
	c.calls++
	return resp, nil
}

func openAITestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     http.StatusText(statusCode),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestBuildOpenAIChatCompletionRequestMarksToolsStrict(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestTestTool{}},
	})

	if len(req.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function == nil {
		t.Fatalf("tool function is nil")
	}
	if !req.Tools[0].Function.Strict {
		t.Fatalf("tool strict = false, want true")
	}
}

func TestBuildOpenAIChatCompletionRequestMapsNamedToolChoice(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		},
	})

	choice, ok := req.ToolChoice.(openaisdk.ToolChoice)
	if !ok {
		t.Fatalf("ToolChoice = %#v, want openai.ToolChoice", req.ToolChoice)
	}
	if choice.Type != openaisdk.ToolTypeFunction {
		t.Fatalf("ToolChoice.Type = %q, want function", choice.Type)
	}
	if choice.Function.Name != "lookup" {
		t.Fatalf("ToolChoice.Function.Name = %q, want lookup", choice.Function.Name)
	}
}

func TestBuildOpenAIChatCompletionRequestMapsResponseFormat(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-4o", llm.NewChatContext(), &llm.ChatOptions{
		ResponseFormat: map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "WeatherAnswer",
				"strict": true,
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"summary": map[string]any{"type": "string"},
					},
					"required":             []string{"summary"},
					"additionalProperties": false,
				},
			},
		},
	})

	if req.ResponseFormat == nil {
		t.Fatal("ResponseFormat = nil, want json_schema response format")
	}
	if req.ResponseFormat.Type != openaisdk.ChatCompletionResponseFormatTypeJSONSchema {
		t.Fatalf("ResponseFormat.Type = %q, want json_schema", req.ResponseFormat.Type)
	}
	if req.ResponseFormat.JSONSchema == nil {
		t.Fatal("ResponseFormat.JSONSchema = nil, want schema")
	}
	if req.ResponseFormat.JSONSchema.Name != "WeatherAnswer" || !req.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("ResponseFormat.JSONSchema = %#v, want strict WeatherAnswer", req.ResponseFormat.JSONSchema)
	}
}

func TestBuildOpenAIChatCompletionRequestDropsUnsupportedReasoningParams(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("openai/gpt-5", llm.NewChatContext(), &llm.ChatOptions{
		ParallelToolCalls: true,
		ExtraParams: map[string]any{
			"temperature":           0.7,
			"top_p":                 0.8,
			"presence_penalty":      0.1,
			"frequency_penalty":     0.2,
			"n":                     2,
			"logit_bias":            map[string]any{"42": 7.0},
			"logprobs":              true,
			"top_logprobs":          3,
			"reasoning_effort":      "low",
			"max_completion_tokens": 128,
			"service_tier":          "priority",
			"stop":                  []string{"END"},
		},
	})

	if req.Temperature != 0 || req.TopP != 0 || req.PresencePenalty != 0 || req.FrequencyPenalty != 0 {
		t.Fatalf("sampling params = %v/%v/%v/%v, want dropped zero values", req.Temperature, req.TopP, req.PresencePenalty, req.FrequencyPenalty)
	}
	if req.N != 0 || req.LogitBias != nil || req.LogProbs || req.TopLogProbs != 0 {
		t.Fatalf("unsupported params not dropped: N=%d LogitBias=%#v LogProbs=%v TopLogProbs=%d", req.N, req.LogitBias, req.LogProbs, req.TopLogProbs)
	}
	if req.ReasoningEffort != "low" {
		t.Fatalf("ReasoningEffort = %q, want preserved for reasoning model without tools", req.ReasoningEffort)
	}
	if req.MaxCompletionTokens != 128 || req.ServiceTier != openaisdk.ServiceTierPriority || len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Fatalf("supported params = max_completion_tokens %d service_tier %q stop %#v, want preserved", req.MaxCompletionTokens, req.ServiceTier, req.Stop)
	}
}

func TestBuildOpenAIChatCompletionRequestDefaultsReasoningEffort(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{model: "gpt-5", want: "minimal"},
		{model: "gpt-5-mini", want: "minimal"},
		{model: "gpt-5.1", want: "none"},
		{model: "gpt-5.2", want: "none"},
		{model: "gpt-4.1", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			req := buildOpenAIChatCompletionRequest(tc.model, llm.NewChatContext(), &llm.ChatOptions{
				ParallelToolCalls: true,
			})
			if req.ReasoningEffort != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q", req.ReasoningEffort, tc.want)
			}
		})
	}
}

func TestBuildOpenAIChatCompletionRequestDropsReasoningEffortWithIncompatibleTools(t *testing.T) {
	req := buildOpenAIChatCompletionRequest("gpt-5.2", llm.NewChatContext(), &llm.ChatOptions{
		Tools: []llm.Tool{requestTestTool{}},
		ExtraParams: map[string]any{
			"reasoning_effort":      "low",
			"max_completion_tokens": 128,
		},
	})

	if req.ReasoningEffort != "" {
		t.Fatalf("ReasoningEffort = %q, want dropped for gpt-5.2 with tools", req.ReasoningEffort)
	}
	if req.MaxCompletionTokens != 128 {
		t.Fatalf("MaxCompletionTokens = %d, want preserved", req.MaxCompletionTokens)
	}
}

func TestOpenAICompletionUsageHandlesMissingTokenDetails(t *testing.T) {
	usage := openAICompletionUsage(&openaisdk.Usage{
		CompletionTokens: 7,
		PromptTokens:     11,
		TotalTokens:      18,
	})

	if usage == nil {
		t.Fatal("openAICompletionUsage() = nil, want usage")
	}
	if usage.CompletionTokens != 7 || usage.PromptTokens != 11 || usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v, want base token counts", usage)
	}
	if usage.PromptCachedTokens != 0 {
		t.Fatalf("PromptCachedTokens = %d, want 0 without prompt token details", usage.PromptCachedTokens)
	}
}

func TestOpenAICompletionUsageMapsCachedPromptTokens(t *testing.T) {
	usage := openAICompletionUsage(&openaisdk.Usage{
		PromptTokensDetails: &openaisdk.PromptTokensDetails{CachedTokens: 5},
	})

	if usage == nil {
		t.Fatal("openAICompletionUsage() = nil, want usage")
	}
	if usage.PromptCachedTokens != 5 {
		t.Fatalf("PromptCachedTokens = %d, want 5", usage.PromptCachedTokens)
	}
}
