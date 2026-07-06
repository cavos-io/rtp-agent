package anthropic

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/browser"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type captureRoundTripper struct {
	body         map[string]any
	reqURL       string
	headers      http.Header
	hasDeadline  bool
	remaining    time.Duration
	statusCode   int
	responseBody string
	header       http.Header
	err          error
}

func (rt *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	rt.reqURL = req.URL.String()
	rt.headers = req.Header.Clone()
	deadline, ok := req.Context().Deadline()
	rt.hasDeadline = ok
	if ok {
		rt.remaining = time.Until(deadline)
	}
	if err := json.NewDecoder(req.Body).Decode(&rt.body); err != nil {
		return nil, err
	}
	if rt.err != nil {
		return nil, rt.err
	}
	statusCode := rt.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	header := rt.header
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(rt.responseBody)),
		Header:     header,
		Request:    req,
	}, nil
}

type sequenceRoundTripper struct {
	responses []*http.Response
	calls     int
}

func (rt *sequenceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	if rt.calls >= len(rt.responses) {
		return nil, errors.New("unexpected HTTP call")
	}
	resp := rt.responses[rt.calls]
	resp.Request = req
	rt.calls++
	return resp, nil
}

func anthropicTestResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type anthropicCloseErrorBody struct {
	closed bool
}

func (b *anthropicCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *anthropicCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

type anthropicReadErrorBody struct {
	payload string
	err     error
}

func (b *anthropicReadErrorBody) Read(p []byte) (int, error) {
	if b.payload != "" {
		n := copy(p, b.payload)
		b.payload = b.payload[n:]
		return n, nil
	}
	return 0, b.err
}

func (b *anthropicReadErrorBody) Close() error {
	return nil
}

type anthropicSignalCloseErrorBody struct {
	err       error
	closeCh   chan struct{}
	closeOnce sync.Once
}

func (b *anthropicSignalCloseErrorBody) Read(_ []byte) (int, error) {
	return 0, b.err
}

func (b *anthropicSignalCloseErrorBody) Close() error {
	b.closeOnce.Do(func() {
		close(b.closeCh)
	})
	return nil
}

type anthropicTimeoutError struct{}

func (anthropicTimeoutError) Error() string {
	return "read timeout"
}

func (anthropicTimeoutError) Timeout() bool {
	return true
}

func (anthropicTimeoutError) Temporary() bool {
	return false
}

type anthropicSingleCloseBody struct {
	closed bool
}

var _ net.Error = anthropicTimeoutError{}

func (b *anthropicSingleCloseBody) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (b *anthropicSingleCloseBody) Close() error {
	if b.closed {
		return errors.New("closed twice")
	}
	b.closed = true
	return nil
}

type anthropicTrackedBody struct {
	reader strings.Reader
	closed bool
}

func newAnthropicTrackedBody(payload string) *anthropicTrackedBody {
	return &anthropicTrackedBody{reader: *strings.NewReader(payload)}
}

func (b *anthropicTrackedBody) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *anthropicTrackedBody) Close() error {
	if b.closed {
		return errors.New("closed twice")
	}
	b.closed = true
	return nil
}

func TestAnthropicLLMMetadataMatchesReference(t *testing.T) {
	model, err := NewAnthropicLLM("test-key", "")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	if got := model.Model(); got != defaultAnthropicMode {
		t.Fatalf("Model() = %q, want %q", got, defaultAnthropicMode)
	}
	if got := model.Provider(); got != "api.anthropic.com" {
		t.Fatalf("Provider() = %q, want reference base URL host", got)
	}

	custom, err := NewAnthropicLLM("test-key", "", WithAnthropicBaseURL("https://anthropic.example/v1/"))
	if err != nil {
		t.Fatalf("NewAnthropicLLM(custom base URL) error = %v", err)
	}
	if got := custom.Provider(); got != "anthropic.example" {
		t.Fatalf("custom Provider() = %q, want configured base URL host", got)
	}
}

func TestAnthropicChatReturnsAPITimeoutErrorOnTransportDeadline(t *testing.T) {
	transport := &captureRoundTripper{err: context.DeadlineExceeded}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
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

func TestAnthropicChatReturnsAPITimeoutErrorOnTransportTimeout(t *testing.T) {
	transport := &captureRoundTripper{err: anthropicTimeoutError{}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
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

func TestAnthropicChatReturnsAPIConnectionErrorOnTransportError(t *testing.T) {
	transport := &captureRoundTripper{err: errors.New("dial failed")}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	var timeoutErr *llm.APITimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("Chat error = %T %v, want non-timeout APIConnectionError", err, err)
	}
	if connectionErr.Message != "dial failed" {
		t.Fatalf("Message = %q, want transport error message", connectionErr.Message)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want connection errors retryable")
	}
}

type anthropicRequestTestTool struct{}

func (anthropicRequestTestTool) ID() string          { return "lookup" }
func (anthropicRequestTestTool) Name() string        { return "lookup" }
func (anthropicRequestTestTool) Description() string { return "look up information" }
func (anthropicRequestTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
		"required": []string{"query"},
	}
}
func (anthropicRequestTestTool) Execute(context.Context, string) (string, error) {
	return "", nil
}

type anthropicProviderTool struct {
	name     string
	betaFlag string
}

func (t anthropicProviderTool) ID() string          { return t.name }
func (t anthropicProviderTool) Name() string        { return t.name }
func (t anthropicProviderTool) Description() string { return "provider tool" }
func (t anthropicProviderTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t anthropicProviderTool) Execute(context.Context, string) (string, error) {
	return "", nil
}
func (t anthropicProviderTool) AnthropicToolSpec() map[string]interface{} {
	return map[string]interface{}{
		"type": t.name,
		"name": t.name,
	}
}
func (t anthropicProviderTool) AnthropicBetaFlag() string { return t.betaFlag }

func TestAnthropicChatDoesNotApplyConnectOptionsTimeoutToRequestContext(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{Timeout: 75 * time.Millisecond}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if transport.hasDeadline {
		t.Fatalf("request context deadline remaining = %v, want no whole-stream deadline from connect timeout", transport.remaining)
	}
}

func TestAnthropicChatDoesNotApplyDefaultConnectOptionsTimeoutToRequestContext(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if transport.hasDeadline {
		t.Fatalf("request context deadline remaining = %v, want no whole-stream deadline from default connect timeout", transport.remaining)
	}
}

func TestAnthropicChatPreservesCallerDeadlineLikeReference(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stream, err := model.Chat(ctx, llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if !transport.hasDeadline {
		t.Fatal("request context has no deadline, want caller deadline preserved")
	}
	if transport.remaining <= 0 || transport.remaining > time.Minute {
		t.Fatalf("request context deadline remaining = %v, want caller deadline", transport.remaining)
	}
}

func TestAnthropicChatReturnsAPIStatusErrorOnHTTPError(t *testing.T) {
	transport := &captureRoundTripper{
		statusCode:   http.StatusTooManyRequests,
		responseBody: "rate limit",
		header:       http.Header{"request-id": []string{"req_123"}},
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
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
	if statusErr.StatusCode != http.StatusTooManyRequests || statusErr.RequestID != "req_123" {
		t.Fatalf("status metadata = %#v, want 429 req_123", statusErr)
	}
	if statusErr.Body != "rate limit" {
		t.Fatalf("Body = %#v, want response body", statusErr.Body)
	}
	if !statusErr.Retryable {
		t.Fatal("Retryable = false, want 429 retryable")
	}
}

func TestAnthropicChatClientClosedStatusReturnsEOF(t *testing.T) {
	transport := &captureRoundTripper{
		statusCode:   499,
		responseBody: `{"error":{"message":"client closed"}}`,
		header:       http.Header{"request-id": []string{"req_499"}},
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v, want nil for client-closed status", err)
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want EOF for client-closed status", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
}

func TestAnthropicChatReturnsParsedAPIStatusErrorBody(t *testing.T) {
	transport := &captureRoundTripper{
		statusCode:   http.StatusBadRequest,
		responseBody: `{"type":"error","error":{"type":"invalid_request_error","message":"missing messages"}}`,
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.Message != "missing messages (400 Bad Request)" {
		t.Fatalf("Message = %q, want nested Anthropic error message", statusErr.Message)
	}
	body, ok := statusErr.Body.(map[string]any)
	if !ok {
		t.Fatalf("Body = %T %#v, want parsed JSON map", statusErr.Body, statusErr.Body)
	}
	errorBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("Body[error] = %T %#v, want parsed nested error", body["error"], body["error"])
	}
	if errorBody["type"] != "invalid_request_error" {
		t.Fatalf("Body[error][type] = %#v, want invalid_request_error", errorBody["type"])
	}
}

func TestAnthropicChatRetriesRetryableSetupAPIError(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusTooManyRequests, "rate limit"),
		anthropicTestResponse(http.StatusOK, ""),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v, want retry success", err)
	}
	_ = stream.Close()
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial failure plus retry", transport.calls)
	}
}

func TestAnthropicChatReportsExhaustedRetryAsConnectionErrorLikeReference(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusTooManyRequests, "rate limit"),
		anthropicTestResponse(http.StatusTooManyRequests, "still limited"),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Chat error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "failed to generate LLM completion after 2 attempts" {
		t.Fatalf("Message = %q, want exhausted retry message", connectionErr.Message)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial failure plus final retry", transport.calls)
	}
}

func TestAnthropicChatDoesNotRetryNonRetryableSetupAPIError(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusBadRequest, "bad request"),
		anthropicTestResponse(http.StatusOK, ""),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)

	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Chat error = %T %v, want APIStatusError", err, err)
	}
	if transport.calls != 1 {
		t.Fatalf("HTTP calls = %d, want no retry for non-retryable status", transport.calls)
	}
}

func TestAnthropicChatRejectsMalformedToolCallArguments(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "lookup"}}},
		&llm.FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":`},
		&llm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
	}
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	_, err = model.Chat(context.Background(), ctx)

	if err == nil {
		t.Fatalf("Chat() error = %v, want malformed tool arguments error", err)
	}
	if transport.reqURL != "" {
		t.Fatalf("request URL = %q, want no HTTP request after malformed tool arguments", transport.reqURL)
	}
}

func TestBuildAnthropicMessagesMapsEmptyToolArgumentsToObject(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "lookup"}}},
		&llm.FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: ""},
		&llm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
	}

	messages, _, err := buildAnthropicMessagesE(ctx)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesE() error = %v, want nil for empty tool arguments", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want user, assistant tool, user result: %#v", len(messages), messages)
	}
	toolUse := messages[1].Content[0]
	if toolUse.Type != "tool_use" || toolUse.Name != "lookup" || toolUse.ID != "call_lookup" {
		t.Fatalf("tool use = %#v, want lookup call", toolUse)
	}
	inputMap, ok := toolUse.Input.(map[string]any)
	if !ok {
		t.Fatalf("tool input = %#v, want empty object", toolUse.Input)
	}
	if len(inputMap) != 0 {
		t.Fatalf("tool input = %#v, want empty object", toolUse.Input)
	}
}

func TestBuildAnthropicMessagesKeepsNonObjectToolArgumentsLikeReference(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "lookup"}}},
		&llm.FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `["Paris"]`},
		&llm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "ok"},
	}

	messages, _, err := buildAnthropicMessagesE(ctx)
	if err != nil {
		t.Fatalf("buildAnthropicMessagesE() error = %v, want nil for JSON array tool arguments", err)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want user, assistant tool, user result: %#v", len(messages), messages)
	}
	raw, err := json.Marshal(messages[1].Content[0])
	if err != nil {
		t.Fatalf("Marshal tool use block error = %v", err)
	}
	var block map[string]any
	if err := json.Unmarshal(raw, &block); err != nil {
		t.Fatalf("Unmarshal tool use block error = %v", err)
	}
	input, ok := block["input"].([]any)
	if !ok || len(input) != 1 || input[0] != "Paris" {
		t.Fatalf("tool input = %#v, want JSON array argument preserved", block["input"])
	}
}

func TestAnthropicChatRejectsMalformedImageContent(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Image: &llm.ImageContent{Image: "data:image/png;base64,not-valid-base64"}},
			},
		},
	}
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	_, err = model.Chat(context.Background(), ctx)

	if err == nil {
		t.Fatalf("Chat() error = %v, want image serialization error", err)
	}
	if transport.reqURL != "" {
		t.Fatalf("request URL = %q, want no HTTP request after malformed image", transport.reqURL)
	}
}

func TestBuildAnthropicMessagesGroupsToolCallsWithResults(t *testing.T) {
	ctx := llm.NewChatContext()
	groupID := "assistant-turn"
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: groupID, Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: groupID + "/tool-1", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCall{ID: groupID + "/tool-2", CallID: "call_weather", Name: "weather", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris"},
		&llm.FunctionCallOutput{ID: "weather-output", CallID: "call_weather", Name: "weather", Output: "sunny"},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "" {
		t.Fatalf("system = %q, want empty", system)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("first role = %q, want user", messages[0].Role)
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "(empty)")
	if messages[1].Role != "assistant" {
		t.Fatalf("second role = %q, want assistant", messages[1].Role)
	}
	assertAnthropicTextBlock(t, messages[1].Content, 0, "checking")
	assertAnthropicToolUseBlock(t, messages[1].Content, 1, "call_lookup", "lookup")
	assertAnthropicToolUseBlock(t, messages[1].Content, 2, "call_weather", "weather")
	if messages[2].Role != "user" {
		t.Fatalf("third role = %q, want user", messages[2].Role)
	}
	assertAnthropicToolResultBlock(t, messages[2].Content, 0, "call_lookup", "Paris", false)
	assertAnthropicToolResultBlock(t, messages[2].Content, 1, "call_weather", "sunny", false)
}

func TestBuildAnthropicMessagesRejectsDuplicateGroupedAssistantMessages(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant-turn/first", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "first"}}},
		&llm.ChatMessage{ID: "assistant-turn/second", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "second"}}},
	}

	_, _, err := buildAnthropicMessagesE(ctx)

	if err == nil {
		t.Fatal("buildAnthropicMessagesE() error = nil, want duplicate grouped assistant message error")
	}
	if !strings.Contains(err.Error(), "only one message is allowed in a group") {
		t.Fatalf("error = %v, want duplicate grouped assistant message error", err)
	}
}

func TestBuildAnthropicMessagesKeepsDuplicateMatchingToolOutputs(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant-turn", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output-1", CallID: "call_lookup", Name: "lookup", Output: "first"},
		&llm.FunctionCallOutput{ID: "lookup-output-2", CallID: "call_lookup", Name: "lookup", Output: "second"},
	}

	messages, _ := buildAnthropicMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want dummy user, assistant call, duplicate tool results: %#v", len(messages), messages)
	}
	if messages[2].Role != "user" {
		t.Fatalf("third role = %q, want user", messages[2].Role)
	}
	if len(messages[2].Content) != 2 {
		t.Fatalf("tool results = %#v, want both duplicate matching outputs", messages[2].Content)
	}
	assertAnthropicToolResultBlock(t, messages[2].Content, 0, "call_lookup", "first", false)
	assertAnthropicToolResultBlock(t, messages[2].Content, 1, "call_lookup", "second", false)
}

func TestAnthropicChatSendsToolResultIsErrorFalseLikeReference(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "assistant-turn", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "checking"}}},
		&llm.FunctionCall{ID: "assistant-turn/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{"city":"Paris"}`},
		&llm.FunctionCallOutput{ID: "lookup-output", CallID: "call_lookup", Name: "lookup", Output: "Paris", IsError: false},
	}
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	messages, ok := transport.body["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %#v, want dummy user, assistant tool, user result", transport.body["messages"])
	}
	userResult, ok := messages[2].(map[string]any)
	if !ok {
		t.Fatalf("messages[2] = %#v, want map", messages[2])
	}
	content, ok := userResult["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("tool result content = %#v, want one block", userResult["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("tool result block = %#v, want map", content[0])
	}
	value, ok := block["is_error"]
	if !ok {
		t.Fatalf("tool result block = %#v, want explicit is_error false", block)
	}
	if value != false {
		t.Fatalf("is_error = %#v, want false", value)
	}
}

func TestBuildAnthropicMessagesParsesJSONListToolResultContent(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "look"}}},
		&llm.FunctionCall{ID: "assistant/tool", CallID: "call_computer", Name: "computer", Arguments: `{"action":"screenshot"}`},
		&llm.FunctionCallOutput{
			ID:     "output",
			CallID: "call_computer",
			Name:   "computer",
			Output: `[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cG5n"}}]`,
		},
	}

	messages, _ := buildAnthropicMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want user, assistant tool, user result: %#v", len(messages), messages)
	}
	result := messages[2].Content[0]
	content, ok := result.Content.([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("tool result content = %#v, want structured list", result.Content)
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("tool result content[0] = %#v, want map", content[0])
	}
	if block["type"] != "image" {
		t.Fatalf("tool result block = %#v, want image block", block)
	}
}

func TestBuildAnthropicMessagesKeepsJSONNullToolResultTextLikeReference(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "look"}}},
		&llm.FunctionCall{ID: "assistant/tool", CallID: "call_lookup", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "output", CallID: "call_lookup", Name: "lookup", Output: "null"},
	}

	messages, _ := buildAnthropicMessages(ctx)

	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want user, assistant tool, user result: %#v", len(messages), messages)
	}
	result := messages[2].Content[0]
	if result.Content != "null" {
		t.Fatalf("tool result content = %#v, want string null", result.Content)
	}
}

func TestBuildAnthropicMessagesFiltersUnmatchedToolItems(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.FunctionCall{ID: "orphan-call", CallID: "call_missing_output", Name: "lookup", Arguments: `{}`},
		&llm.FunctionCallOutput{ID: "orphan-output", CallID: "call_missing_call", Name: "lookup", Output: "ignored"},
	}

	messages, _ := buildAnthropicMessages(ctx)

	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("role = %q, want user", messages[0].Role)
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "hello")
}

func TestAnthropicStreamMapsCacheUsageMetadata(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":11,"cache_creation_input_tokens":3,"cache_read_input_tokens":5}}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.ID != "msg_1" {
		t.Fatalf("chunk ID = %q, want msg_1", chunk.ID)
	}
	if chunk.Usage == nil {
		t.Fatal("Usage = nil, want usage metadata")
	}
	if chunk.Usage.PromptTokens != 19 || chunk.Usage.CacheCreationTokens != 3 || chunk.Usage.CacheReadTokens != 5 {
		t.Fatalf("Usage = %#v, want prompt and cache token counts", chunk.Usage)
	}
}

func TestAnthropicStreamEmitsFinalUsageAfterText(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":11,"cache_creation_input_tokens":3,"cache_read_input_tokens":5}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"message_delta","usage":{"output_tokens":7}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	text, err := stream.Next()
	if err != nil {
		t.Fatalf("text Next() error = %v", err)
	}
	if text.ID != "msg_1" || text.Delta == nil || text.Delta.Content != "hello" {
		t.Fatalf("first chunk = %#v, want text delta with request ID", text)
	}

	usage, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next() error = %v", err)
	}
	if usage.ID != "msg_1" {
		t.Fatalf("usage ID = %q, want msg_1", usage.ID)
	}
	if usage.Usage == nil {
		t.Fatal("Usage = nil, want final usage metadata")
	}
	if usage.Usage.PromptTokens != 19 || usage.Usage.CompletionTokens != 7 || usage.Usage.TotalTokens != 26 {
		t.Fatalf("Usage = %#v, want accumulated prompt/completion/total tokens", usage.Usage)
	}
	if usage.Usage.CacheCreationTokens != 3 || usage.Usage.CacheReadTokens != 5 || usage.Usage.PromptCachedTokens != 5 {
		t.Fatalf("cache Usage = %#v, want accumulated cache token counts", usage.Usage)
	}

	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Next() error = %v, want EOF", err)
	}
}

func TestAnthropicStreamEmitsFinalUsageOnEOFWithoutMessageStop(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":11,"cache_creation_input_tokens":3,"cache_read_input_tokens":5}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"message_delta","usage":{"output_tokens":7}}`,
			``,
		}, "\n"))),
	}

	text, err := stream.Next()
	if err != nil {
		t.Fatalf("text Next() error = %v", err)
	}
	if text.Delta == nil || text.Delta.Content != "hello" {
		t.Fatalf("first chunk = %#v, want text delta", text)
	}

	usage, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next() error = %v", err)
	}
	if usage.ID != "msg_1" {
		t.Fatalf("usage ID = %q, want msg_1", usage.ID)
	}
	if usage.Usage == nil {
		t.Fatal("Usage = nil, want final usage metadata")
	}
	if usage.Usage.PromptTokens != 19 || usage.Usage.CompletionTokens != 7 || usage.Usage.TotalTokens != 26 {
		t.Fatalf("Usage = %#v, want accumulated prompt/completion/total tokens", usage.Usage)
	}
}

func TestAnthropicStreamEmitsZeroUsageOnEmptyEOF(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader("")),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final usage chunk", err)
	}
	if chunk.ID != "" {
		t.Fatalf("chunk ID = %q, want empty request ID", chunk.ID)
	}
	if chunk.Usage == nil {
		t.Fatal("Usage = nil, want zero final usage")
	}
	if chunk.Usage.PromptTokens != 0 || chunk.Usage.CompletionTokens != 0 || chunk.Usage.TotalTokens != 0 {
		t.Fatalf("Usage = %#v, want zero usage", chunk.Usage)
	}
}

func TestAnthropicStreamReturnsAPIErrorOnErrorEvent(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(`data: {"type":"error","error":{"type":"overloaded_error","message":"server overloaded"}}` + "\n\n")),
	}

	_, err := stream.Next()

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if apiErr.Message != "server overloaded" {
		t.Fatalf("Message = %q, want nested Anthropic stream error message", apiErr.Message)
	}
	body, ok := apiErr.Body.(map[string]any)
	if !ok {
		t.Fatalf("Body = %T %#v, want parsed JSON map", apiErr.Body, apiErr.Body)
	}
	if errorBody, ok := body["error"].(map[string]any); !ok || errorBody["type"] != "overloaded_error" {
		t.Fatalf("Body[error] = %#v, want overloaded_error", body["error"])
	}
	if !apiErr.Retryable {
		t.Fatal("Retryable = false, want stream API errors retryable")
	}
}

func TestAnthropicStreamErrorAfterChunkIsNotRetryable(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			`data: {"type":"error","error":{"type":"overloaded_error","message":"server overloaded"}}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("text Next() error = %v", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != "hello" {
		t.Fatalf("first chunk = %#v, want text delta", chunk)
	}

	_, err = stream.Next()

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIError", err, err)
	}
	if apiErr.Retryable {
		t.Fatal("Retryable = true after visible output, want false")
	}
}

func TestAnthropicChatRetriesErrorEventBeforeChunkLikeReference(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusOK, `data: {"type":"error","error":{"type":"overloaded_error","message":"server overloaded"}}`+"\n\n"),
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"retry ok"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want retry success", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != "retry ok" {
		t.Fatalf("chunk = %#v, want retried visible text", chunk)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial stream plus retry", transport.calls)
	}
}

func TestAnthropicStreamMalformedEventReturnsConnectionError(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":`,
			``,
		}, "\n"))),
	}

	_, err := stream.Next()

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false before visible output, want true")
	}
}

func TestAnthropicChatRetriesMalformedEventBeforeChunkLikeReference(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":`,
			``,
		}, "\n")),
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"retry ok"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want retry success", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != "retry ok" {
		t.Fatalf("chunk = %#v, want retried visible text", chunk)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial stream plus retry", transport.calls)
	}
}

func TestAnthropicStreamInputDeltaWithoutToolStartReturnsConnectionError(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"city\""}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	_, err := stream.Next()

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false before visible output, want true")
	}
}

func TestAnthropicChatRetriesInputDeltaWithoutToolStartLikeReference(t *testing.T) {
	transport := &sequenceRoundTripper{responses: []*http.Response{
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"city\""}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")),
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"retry ok"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want retry success", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != "retry ok" {
		t.Fatalf("chunk = %#v, want retried visible text", chunk)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial stream plus retry", transport.calls)
	}
}

func TestAnthropicStreamKeepsEmptyToolCallIDActive(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"","name":"lookup"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Paris\"}"}}`,
			`data: {"type":"content_block_stop"}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.Delta == nil || len(chunk.Delta.ToolCalls) != 1 {
		t.Fatalf("chunk Delta = %#v, want one tool call", chunk.Delta)
	}
	call := chunk.Delta.ToolCalls[0]
	if call.CallID != "" || call.Name != "lookup" || call.Arguments != `{"city":"Paris"}` {
		t.Fatalf("tool call = %#v, want empty call ID with streamed arguments", call)
	}
}

func TestAnthropicStreamReadErrorBeforeChunkReturnsAPIConnectionError(t *testing.T) {
	body := &anthropicReadErrorBody{
		payload: `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}` + "\n",
		err:     errors.New("stream reset"),
	}
	stream := &anthropicStream{
		resp:   &http.Response{Body: body},
		reader: bufio.NewReader(body),
	}

	_, err := stream.Next()

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "stream reset" {
		t.Fatalf("Message = %q, want stream reset", connectionErr.Message)
	}
	if !connectionErr.Retryable {
		t.Fatal("Retryable = false, want read errors before output retryable")
	}
}

func TestAnthropicStreamReadTimeoutReturnsAPITimeoutErrorLikeReference(t *testing.T) {
	body := &anthropicReadErrorBody{
		payload: `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}` + "\n",
		err:     anthropicTimeoutError{},
	}
	stream := &anthropicStream{
		resp:   &http.Response{Body: body},
		reader: bufio.NewReader(body),
	}

	_, err := stream.Next()

	var timeoutErr *llm.APITimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("Next() error = %T %v, want APITimeoutError", err, err)
	}
	if timeoutErr.Message != "Request timed out." {
		t.Fatalf("Message = %q, want default timeout message", timeoutErr.Message)
	}
	if !timeoutErr.Retryable {
		t.Fatal("Retryable = false, want timeout before output retryable")
	}
}

func TestAnthropicChatRetriesStreamReadErrorBeforeChunkLikeReference(t *testing.T) {
	firstBody := &anthropicReadErrorBody{
		payload: `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}` + "\n",
		err:     errors.New("stream reset"),
	}
	transport := &sequenceRoundTripper{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: firstBody, Header: make(http.Header)},
		anthropicTestResponse(http.StatusOK, strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"retry ok"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")),
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want retry success", err)
	}
	if chunk.Delta == nil || chunk.Delta.Content != "retry ok" {
		t.Fatalf("chunk = %#v, want retried visible text", chunk)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial stream plus retry", transport.calls)
	}
}

func TestAnthropicChatReportsExhaustedStreamRetryAsConnectionErrorLikeReference(t *testing.T) {
	firstBody := &anthropicReadErrorBody{
		payload: `data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}` + "\n",
		err:     errors.New("stream reset"),
	}
	secondBody := &anthropicReadErrorBody{
		payload: `data: {"type":"message_start","message":{"id":"msg_2","usage":{"input_tokens":3}}}` + "\n",
		err:     errors.New("stream reset again"),
	}
	transport := &sequenceRoundTripper{responses: []*http.Response{
		{StatusCode: http.StatusOK, Body: firstBody, Header: make(http.Header)},
		{StatusCode: http.StatusOK, Body: secondBody, Header: make(http.Header)},
	}}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 1}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	_, err = stream.Next()

	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next() error = %T %v, want APIConnectionError", err, err)
	}
	if connectionErr.Message != "failed to generate LLM completion after 2 attempts" {
		t.Fatalf("Message = %q, want exhausted retry message", connectionErr.Message)
	}
	if transport.calls != 2 {
		t.Fatalf("HTTP calls = %d, want initial stream plus final retry", transport.calls)
	}
}

func TestAnthropicStreamSuppressesReferenceThinkingText(t *testing.T) {
	transport := &captureRoundTripper{
		responseBody: strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"<thinking>hidden"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" still hidden"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"</thinking>visible answer"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"),
	}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), llm.NewChatContext(), llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("visible text Next() error = %v", err)
	}
	if chunk.Delta == nil {
		t.Fatalf("chunk Delta = nil, want visible text")
	}
	if chunk.Delta.Content != "visible answer" {
		t.Fatalf("content = %q, want visible answer without thinking text", chunk.Delta.Content)
	}
}

func TestAnthropicStreamEmitsEmptyThinkingCloseDeltaLikeReference(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"<thinking>hidden"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"</thinking>"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want empty assistant delta", err)
	}
	if chunk.Delta == nil {
		t.Fatal("Delta = nil, want empty assistant delta before final usage")
	}
	if chunk.Delta.Role != llm.ChatRoleAssistant || chunk.Delta.Content != "" {
		t.Fatalf("Delta = %#v, want assistant role with empty content", chunk.Delta)
	}

	usage, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next() error = %v", err)
	}
	if usage.Usage == nil {
		t.Fatalf("usage chunk = %#v, want final usage after empty delta", usage)
	}
}

func TestAnthropicStreamDropsSameChunkThinkingCloseLikeReference(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"<thinking>hidden</thinking>visible answer"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want final usage chunk", err)
	}
	if chunk.Delta != nil {
		t.Fatalf("Delta = %#v, want no visible text from same-chunk thinking close", chunk.Delta)
	}
	if chunk.Usage == nil {
		t.Fatal("Usage = nil, want final usage after suppressed thinking chunk")
	}
}

func TestAnthropicStreamSuppressesReferenceThinkingTextWithoutTools(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"<thinking>hidden"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" still hidden"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"</thinking>visible answer"}}`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("visible text Next() error = %v", err)
	}
	if chunk.Delta == nil {
		t.Fatalf("chunk Delta = nil, want visible text")
	}
	if chunk.Delta.Content != "visible answer" {
		t.Fatalf("content = %q, want visible answer without thinking text", chunk.Delta.Content)
	}
}

func TestAnthropicStreamTextChunkCarriesReferenceRequestID(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("text delta Next() error = %v", err)
	}
	if chunk.ID != "msg_1" {
		t.Fatalf("text chunk ID = %q, want msg_1", chunk.ID)
	}
}

func TestAnthropicStreamParsesSplitSSEDataLikeReference(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			``,
			`data: {"type":"content_block_delta",`,
			`data: "delta":{"type":"text_delta","text":"hello"}}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want split SSE text delta", err)
	}
	if chunk.ID != "msg_1" || chunk.Delta == nil || chunk.Delta.Content != "hello" {
		t.Fatalf("chunk = %#v, want hello text delta with request ID", chunk)
	}
}

func TestAnthropicStreamParsesSSEDataWithoutSpaceLikeReference(t *testing.T) {
	stream := &anthropicStream{
		reader: bufio.NewReader(strings.NewReader(strings.Join([]string{
			`data:{"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
			``,
			`data:{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`,
			``,
		}, "\n"))),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want no-space SSE text delta", err)
	}
	if chunk.ID != "msg_1" || chunk.Delta == nil || chunk.Delta.Content != "hello" {
		t.Fatalf("chunk = %#v, want hello text delta with request ID", chunk)
	}
}

func TestAnthropicStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &anthropicCloseErrorBody{}
	stream := &anthropicStream{
		resp:   &http.Response{Body: body},
		reader: bufio.NewReader(body),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err := stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want io.EOF", err)
	}
}

func TestAnthropicStreamClosesBodyBeforeFinalUsageLikeReference(t *testing.T) {
	body := newAnthropicTrackedBody(strings.Join([]string{
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":3}}}`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n"))
	stream := &anthropicStream{
		resp:   &http.Response{Body: body},
		reader: bufio.NewReader(body),
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("usage Next() error = %v", err)
	}
	if chunk.Usage == nil {
		t.Fatalf("chunk = %#v, want final usage before EOF", chunk)
	}
	if !body.closed {
		t.Fatal("body closed = false, want provider stream body closed before final usage")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() after final usage error = %v, want nil", err)
	}

	_, err = stream.Next()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("EOF Next() error = %v, want io.EOF", err)
	}
	if !body.closed {
		t.Fatal("body closed = false, want provider stream body closed at EOF")
	}
}

func TestAnthropicStreamCloseIsIdempotent(t *testing.T) {
	body := &anthropicSingleCloseBody{}
	stream := &anthropicStream{
		resp:   &http.Response{Body: body},
		reader: bufio.NewReader(body),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
}

func TestAnthropicStreamCloseDuringRetryWaitReturnsEOF(t *testing.T) {
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	body := &anthropicSignalCloseErrorBody{
		err:     errors.New("stream reset"),
		closeCh: make(chan struct{}),
	}
	stream := &anthropicStream{
		llm:            model,
		ctx:            ctx,
		connectOptions: llm.APIConnectOptions{MaxRetry: 2, RetryInterval: time.Hour},
		retryAttempt:   1,
		resp:           &http.Response{Body: body},
		reader:         bufio.NewReader(body),
		cancel:         cancel,
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-body.closeCh:
	case <-time.After(time.Second):
		t.Fatal("stream did not enter retry wait")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Next() error = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next() did not return after Close")
	}
}

func TestAnthropicChatUsesStrictToolInputSchema(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), llm.NewChatContext(), llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	tools, ok := transport.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", transport.body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want map", tools[0])
	}
	if tool["strict"] != true {
		t.Fatalf("tool strict = %#v, want true", tool["strict"])
	}
	inputSchema, ok := tool["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("input_schema = %#v, want map", tool["input_schema"])
	}
	if inputSchema["additionalProperties"] != false {
		t.Fatalf("input_schema additionalProperties = %#v, want false", inputSchema["additionalProperties"])
	}
	if inputSchema["type"] != "object" {
		t.Fatalf("input_schema type = %#v, want object", inputSchema["type"])
	}
}

func TestAnthropicChatUsesProviderComputerToolSpec(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	computerTool := NewComputerTool(browser.NewPageActions(), 1440, 900)
	stream, err := model.Chat(context.Background(), llm.NewChatContext(), llm.WithTools(computerTool.Tools()))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	tools, ok := transport.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one provider tool", transport.body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v, want map", tools[0])
	}
	if tool["type"] != "computer_20251124" || tool["name"] != "computer" {
		t.Fatalf("tool identity = %#v, want 20251124 computer provider tool", tool)
	}
	if tool["display_width_px"] != float64(1440) || tool["display_height_px"] != float64(900) || tool["display_number"] != float64(1) {
		t.Fatalf("tool display config = %#v, want 1440x900 display 1", tool)
	}
	if _, ok := tool["input_schema"]; ok {
		t.Fatalf("provider tool has input_schema = %#v, want provider-native schema", tool["input_schema"])
	}
}

func TestAnthropicChatAddsComputerToolBetaHeader(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	computerTool := NewComputerTool(browser.NewPageActions(), 1440, 900)
	stream, err := model.Chat(context.Background(), llm.NewChatContext(), llm.WithTools(computerTool.Tools()))
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if got := transport.headers.Get("anthropic-beta"); got != "computer-use-2025-11-24" {
		t.Fatalf("anthropic-beta = %q, want computer-use-2025-11-24", got)
	}
}

func TestAnthropicChatUsesLastProviderToolBetaHeaderLikeReference(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{
			anthropicProviderTool{name: "old_computer", betaFlag: "computer-use-2025-01-24"},
			anthropicProviderTool{name: "new_computer", betaFlag: "computer-use-2025-11-24"},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if got := transport.headers.Get("anthropic-beta"); got != "computer-use-2025-11-24" {
		t.Fatalf("anthropic-beta = %q, want last provider tool beta flag", got)
	}
}

func TestAnthropicChatMapsNamedToolChoice(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithToolChoice(map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "lookup",
			},
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	choice, ok := transport.body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want map", transport.body["tool_choice"])
	}
	if choice["type"] != "tool" || choice["name"] != "lookup" {
		t.Fatalf("tool_choice = %#v, want named lookup tool", choice)
	}
}

func TestAnthropicChatOmitsParallelToolChoiceFlagWhenUnset(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithToolChoice("required"),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	choice, ok := transport.body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want map", transport.body["tool_choice"])
	}
	if choice["type"] != "any" {
		t.Fatalf("tool_choice = %#v, want required mapped to any", choice)
	}
	if _, ok := choice["disable_parallel_tool_use"]; ok {
		t.Fatalf("disable_parallel_tool_use = %#v, want omitted when parallel_tool_calls is unset", choice["disable_parallel_tool_use"])
	}
}

func TestAnthropicChatUnknownStringToolChoiceFallsBackToAuto(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithToolChoice("unexpected"),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	choice, ok := transport.body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v, want auto map", transport.body["tool_choice"])
	}
	if choice["type"] != "auto" {
		t.Fatalf("tool_choice = %#v, want reference auto fallback", choice)
	}
}

func TestAnthropicChatToolChoiceNoneClearsTools(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithToolChoice("none"),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	tools, ok := transport.body["tools"].([]any)
	if !ok {
		t.Fatalf("tools = %#v, want empty list", transport.body["tools"])
	}
	if len(tools) != 0 {
		t.Fatalf("tools length = %d, want 0 for tool_choice none", len(tools))
	}
	if _, ok := transport.body["tool_choice"]; ok {
		t.Fatalf("tool_choice = %#v, want omitted for tool_choice none", transport.body["tool_choice"])
	}
}

func TestAnthropicChatOmitsToolChoiceWhenNoTools(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithToolChoice("required"),
		llm.WithParallelToolCalls(false),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if _, ok := transport.body["tools"]; ok {
		t.Fatalf("tools = %#v, want omitted without configured tools", transport.body["tools"])
	}
	if _, ok := transport.body["tool_choice"]; ok {
		t.Fatalf("tool_choice = %#v, want omitted without configured tools", transport.body["tool_choice"])
	}
}

func TestAnthropicChatAppendsTrailingUserForNoPrefillModels(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
	}
	model, err := NewAnthropicLLM("test-key", "")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	stream, err := model.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	messages, ok := transport.body["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v, want list", transport.body["messages"])
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want trailing user turn: %#v", len(messages), messages)
	}
	last, ok := messages[2].(map[string]any)
	if !ok {
		t.Fatalf("last message = %#v, want map", messages[2])
	}
	if last["role"] != "user" {
		t.Fatalf("last role = %#v, want user", last["role"])
	}
	content, ok := last["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("last content = %#v, want one text block", last["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("last content block = %#v, want map", content[0])
	}
	if block["type"] != "text" || block["text"] != " " {
		t.Fatalf("last content block = %#v, want blank reference trailing user text", block)
	}
}

func TestAnthropicDefaultsMatchReference(t *testing.T) {
	model, err := NewAnthropicLLM("test-key", "")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	if model.model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q, want claude-sonnet-4-6", model.model)
	}
}

func TestNewAnthropicLLMFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv(anthropicAPIKeyEnv, "env-key")

	model, err := NewAnthropicLLM("", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v, want nil from env key", err)
	}

	if model.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", model.apiKey)
	}
}

func TestNewAnthropicLLMRequiresAPIKey(t *testing.T) {
	t.Setenv(anthropicAPIKeyEnv, "")

	_, err := NewAnthropicLLM("", "claude-test")

	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("NewAnthropicLLM() error = %v, want API key error", err)
	}
}

func TestAnthropicChatUsesConfiguredBaseURLAndHeaders(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test", WithAnthropicBaseURL("https://anthropic.local/api/"))
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if transport.reqURL != "https://anthropic.local/api/v1/messages" {
		t.Fatalf("request URL = %q, want configured base URL", transport.reqURL)
	}
	if transport.headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", transport.headers.Get("x-api-key"))
	}
	if transport.headers.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("anthropic-version = %q, want reference version", transport.headers.Get("anthropic-version"))
	}
}

func TestAnthropicChatAppliesExtraParams(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{
			"user":        "participant-1",
			"temperature": 0.7,
			"top_k":       40,
			"max_tokens":  2048,
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if transport.body["user"] != "participant-1" {
		t.Fatalf("user = %#v, want participant-1", transport.body["user"])
	}
	if transport.body["temperature"] != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", transport.body["temperature"])
	}
	if transport.body["top_k"] != float64(40) {
		t.Fatalf("top_k = %#v, want 40", transport.body["top_k"])
	}
	if transport.body["max_tokens"] != float64(2048) {
		t.Fatalf("max_tokens = %#v, want 2048", transport.body["max_tokens"])
	}
}

func TestAnthropicChatForwardsReferenceExtraParams(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{
			"stop_sequences": []string{"END"},
			"metadata":       map[string]any{"trace_id": "turn-1"},
			"caching":        "ephemeral",
		}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	if _, ok := transport.body["stop_sequences"]; !ok {
		t.Fatalf("stop_sequences missing from body: %#v", transport.body)
	}
	if _, ok := transport.body["metadata"]; !ok {
		t.Fatalf("metadata missing from body: %#v", transport.body)
	}
	if _, ok := transport.body["caching"]; ok {
		t.Fatalf("caching leaked to provider body: %#v", transport.body["caching"])
	}
}

func TestAnthropicChatRejectsReservedExtraParamModelLikeReference(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{"model": "claude-other"}),
	)

	if err == nil {
		t.Fatal("Chat() error = nil, want reserved extra param error")
	}
	if !strings.Contains(err.Error(), `extra param "model" conflicts with reserved Anthropic request field`) {
		t.Fatalf("Chat() error = %v, want reserved model extra param error", err)
	}
	if transport.reqURL != "" {
		t.Fatalf("request URL = %q, want no HTTP request after reserved extra param error", transport.reqURL)
	}
}

func TestAnthropicChatRejectsUnserializableRequestBody(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}

	_, err = model.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithExtraParams(map[string]any{"temperature": func() {}}),
	)

	if err == nil {
		t.Fatal("Chat() error = nil, want request serialization error")
	}
	if transport.reqURL != "" {
		t.Fatalf("request URL = %q, want no HTTP request after serialization error", transport.reqURL)
	}
}

func TestAnthropicChatSendsSystemMessagesAsTextBlocks(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(context.Background(), ctx)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	system, ok := transport.body["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system = %#v, want one base text block", transport.body["system"])
	}
	assertAnthropicRequestTextBlock(t, system[0], "base")
	messages, ok := transport.body["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v, want one user message", transport.body["messages"])
	}
	user, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("message = %#v, want map", messages[0])
	}
	content, ok := user["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v, want inline developer instructions and user text", user["content"])
	}
	assertAnthropicRequestTextBlock(t, content[0], "<instructions>\ndev\n</instructions>")
	assertAnthropicRequestTextBlock(t, content[1], "hello")
}

func TestAnthropicChatAppliesEphemeralCacheControl(t *testing.T) {
	transport := &captureRoundTripper{}
	originalTransport := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "be fast"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
		&llm.ChatMessage{ID: "assistant", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "hi"}}},
	}
	model, err := NewAnthropicLLM("test-key", "claude-test")
	if err != nil {
		t.Fatalf("NewAnthropicLLM() error = %v", err)
	}
	stream, err := model.Chat(
		context.Background(),
		ctx,
		llm.WithTools([]llm.Tool{anthropicRequestTestTool{}}),
		llm.WithExtraParams(map[string]any{"caching": "ephemeral"}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	_ = stream.Close()

	assertCacheControlBlock(t, transport.body["system"], "system")
	tools, ok := transport.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want one tool", transport.body["tools"])
	}
	assertCacheControlMap(t, tools[0], "tool")

	messages, ok := transport.body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want user and assistant messages", transport.body["messages"])
	}
	user := messages[0].(map[string]any)
	assistant := messages[1].(map[string]any)
	assertCacheControlBlock(t, user["content"], "user content")
	assertCacheControlBlock(t, assistant["content"], "assistant content")
}

func TestBuildAnthropicMessagesCollectsSystemText(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "developer", Role: llm.ChatRoleDeveloper, Content: []llm.ChatContent{{Text: "dev"}}},
		&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "base" {
		t.Fatalf("system = %q, want base", system)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(messages))
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "<instructions>\ndev\n</instructions>")
	assertAnthropicTextBlock(t, messages[0].Content, 1, "hello")
}

func TestBuildAnthropicMessagesConvertsMidConversationInstructions(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "system", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "base"}}},
		&llm.ChatMessage{ID: "old-user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "old question"}}},
		&llm.ChatMessage{ID: "old-assistant", Role: llm.ChatRoleAssistant, Content: []llm.ChatContent{{Text: "old answer"}}},
		&llm.ChatMessage{ID: "turn-instructions", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "use short sentences"}}},
		&llm.ChatMessage{ID: "new-user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "new question"}}},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "base" {
		t.Fatalf("system = %q, want only base instructions", system)
	}
	if len(messages) != 3 {
		t.Fatalf("len(messages) = %d, want old user, assistant, inline instructions/new user: %#v", len(messages), messages)
	}
	if messages[2].Role != "user" {
		t.Fatalf("messages[2].Role = %q, want user inline instructions", messages[2].Role)
	}
	assertAnthropicTextBlock(t, messages[2].Content, 0, "<instructions>\nuse short sentences\n</instructions>")
	assertAnthropicTextBlock(t, messages[2].Content, 1, "new question")
}

func TestBuildAnthropicMessagesCountsEmptyLeadingInstruction(t *testing.T) {
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{ID: "empty-system", Role: llm.ChatRoleSystem},
		&llm.ChatMessage{ID: "turn-instructions", Role: llm.ChatRoleSystem, Content: []llm.ChatContent{{Text: "answer briefly"}}},
	}

	messages, system := buildAnthropicMessages(ctx)

	if system != "" {
		t.Fatalf("system = %q, want empty", system)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want inline instruction user message: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("messages[0].Role = %q, want user", messages[0].Role)
	}
	assertAnthropicTextBlock(t, messages[0].Content, 0, "<instructions>\nanswer briefly\n</instructions>")
}

func TestBuildAnthropicMessagesIncludesImageBlocks(t *testing.T) {
	imageData := base64.StdEncoding.EncodeToString([]byte("gif-bytes"))
	ctx := llm.NewChatContext()
	ctx.Items = []llm.ChatItem{
		&llm.ChatMessage{
			ID:   "user",
			Role: llm.ChatRoleUser,
			Content: []llm.ChatContent{
				{Text: "describe"},
				{Image: &llm.ImageContent{Image: "data:image/gif;base64," + imageData}},
				{Image: &llm.ImageContent{Image: "https://example.test/image.png"}},
			},
		},
	}

	messages, _ := buildAnthropicMessages(ctx)

	blocks := messages[0].Content
	if len(blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3: %#v", len(blocks), blocks)
	}
	inlineBlock := anthropicContentBlockToMap(t, blocks[1])
	if inlineBlock["type"] != "image" {
		t.Fatalf("inline image block = %#v", inlineBlock)
	}
	inlineSource := inlineBlock["source"].(map[string]any)
	if inlineSource["type"] != "base64" || inlineSource["data"] != imageData || inlineSource["media_type"] != "image/gif" {
		t.Fatalf("inline image source = %#v", inlineSource)
	}
	urlBlock := anthropicContentBlockToMap(t, blocks[2])
	if urlBlock["type"] != "image" {
		t.Fatalf("url image block = %#v", urlBlock)
	}
	urlSource := urlBlock["source"].(map[string]any)
	if urlSource["type"] != "url" || urlSource["url"] != "https://example.test/image.png" {
		t.Fatalf("url image source = %#v", urlSource)
	}
}

func anthropicContentBlockToMap(t *testing.T, block anthropicContentBlock) map[string]any {
	t.Helper()
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal block: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal block: %v", err)
	}
	return out
}

func assertCacheControlBlock(t *testing.T, raw any, label string) {
	t.Helper()
	blocks, ok := raw.([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("%s = %#v, want non-empty block list", label, raw)
	}
	assertCacheControlMap(t, blocks[len(blocks)-1], label)
}

func assertCacheControlMap(t *testing.T, raw any, label string) {
	t.Helper()
	block, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want map", label, raw)
	}
	cacheControl, ok := block["cache_control"].(map[string]any)
	if !ok {
		t.Fatalf("%s cache_control = %#v, want map", label, block["cache_control"])
	}
	if cacheControl["type"] != "ephemeral" {
		t.Fatalf("%s cache_control.type = %#v, want ephemeral", label, cacheControl["type"])
	}
}

func assertAnthropicRequestTextBlock(t *testing.T, raw any, want string) {
	t.Helper()
	block, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("system block = %#v, want map", raw)
	}
	if block["type"] != "text" || block["text"] != want {
		t.Fatalf("system block = %#v, want text %q", block, want)
	}
}

func assertAnthropicTextBlock(t *testing.T, blocks []anthropicContentBlock, index int, want string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	if blocks[index].Type != "text" || blocks[index].Text != want {
		t.Fatalf("text block[%d] = %#v, want %q", index, blocks[index], want)
	}
}

func assertAnthropicToolUseBlock(t *testing.T, blocks []anthropicContentBlock, index int, wantID, wantName string) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	if blocks[index].Type != "tool_use" || blocks[index].ID != wantID || blocks[index].Name != wantName {
		t.Fatalf("tool use block[%d] = %#v", index, blocks[index])
	}
}

func assertAnthropicToolResultBlock(t *testing.T, blocks []anthropicContentBlock, index int, wantID, wantContent string, wantError bool) {
	t.Helper()
	if len(blocks) <= index {
		t.Fatalf("len(blocks) = %d, want index %d", len(blocks), index)
	}
	gotError := blocks[index].IsError != nil && *blocks[index].IsError
	if blocks[index].Type != "tool_result" || blocks[index].ToolUseID != wantID || blocks[index].Content != wantContent || gotError != wantError {
		t.Fatalf("tool result block[%d] = %#v", index, blocks[index])
	}
}
