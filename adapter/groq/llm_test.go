package groq

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/openai"
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
	if provider.Provider() != "api.groq.com" {
		t.Fatalf("provider = %q, want reference base URL host", provider.Provider())
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
	if provider.Provider() != "groq.example" {
		t.Fatalf("provider = %q, want configured base URL host", provider.Provider())
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

func TestGroqLLMForwardsReferenceOpenAIOptions(t *testing.T) {
	var requestBody string
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		requestBody = string(body)
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     http.StatusText(http.StatusBadRequest),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`)),
			Request:    r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
		WithGroqLLMOptions(
			openai.WithOpenAILLMParallelToolCalls(false),
			openai.WithOpenAILLMToolChoice("none"),
		),
	)

	stream, err := provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	defer stream.Close()
	_, _ = stream.Next()

	if !strings.Contains(requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want provider parallel_tool_calls false", requestBody)
	}
	if !strings.Contains(requestBody, `"tool_choice":"none"`) {
		t.Fatalf("request body = %s, want provider tool_choice none", requestBody)
	}
}

func TestGroqLLMAppliesReferenceTimeoutOption(t *testing.T) {
	var hasDeadline bool
	var remaining time.Duration
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		hasDeadline = ok
		if ok {
			remaining = time.Until(deadline)
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     http.StatusText(http.StatusBadRequest),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`)),
			Request:    r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
		WithGroqLLMTimeout(75*time.Millisecond),
	)

	stream, err := provider.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	defer stream.Close()
	_, _ = stream.Next()

	if !hasDeadline {
		t.Fatal("request context has no deadline, want Groq LLM timeout option applied")
	}
	if remaining <= 0 || remaining > 75*time.Millisecond {
		t.Fatalf("request context deadline remaining = %v, want bounded by configured timeout", remaining)
	}
}

func TestGroqLLMAppliesReferenceMaxRetriesOption(t *testing.T) {
	attempts := 0
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     http.StatusText(http.StatusInternalServerError),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"try again","type":"server_error","code":"server_error"}}`)),
				Request:    r,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"id":"chatcmpl-retry","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"}}]}` + "\n\n" +
					"data: [DONE]\n\n")),
			Request: r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
		WithGroqLLMMaxRetries(1),
	)

	stream, err := provider.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat returned error = %v, want retry success", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error = %v, want retried content", err)
	}
	if chunk == nil || chunk.Delta == nil || chunk.Delta.Content != "ok" {
		t.Fatalf("chunk = %#v, want retried content ok", chunk)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want first failure plus one retry", attempts)
	}
}

func TestNewGroqLLMDoesNotRetryByDefault(t *testing.T) {
	attempts := 0
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     http.StatusText(http.StatusInternalServerError),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"try again","type":"server_error","code":"server_error"}}`)),
			Request:    r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	defer stream.Close()
	_, err = stream.Next()
	if err == nil {
		t.Fatal("Next error is nil, want first provider failure returned without default retry")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want no default provider retry", attempts)
	}
}

func TestGroqLLMStartupErrorUnregistersLazyStream(t *testing.T) {
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     http.StatusText(http.StatusInternalServerError),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"try again","type":"server_error","code":"server_error"}}`)),
			Request:    r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	if len(provider.streams) != 1 {
		t.Fatalf("registered streams = %d, want lazy stream registered", len(provider.streams))
	}
	if _, err := stream.Next(); err == nil {
		t.Fatal("Next error is nil, want provider startup error")
	}
	if len(provider.streams) != 0 {
		t.Fatalf("registered streams after startup error = %d, want unregistered", len(provider.streams))
	}
}

func TestGroqLLMProviderCloseClosesActiveStreams(t *testing.T) {
	calls := 0
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		calls++
		body := io.NopCloser(strings.NewReader(
			`data: {"id":"chatcmpl-groq","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}` + "\n\n" +
				"data: [DONE]\n\n"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
			Request:    r,
		}, nil
	})

	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatalf("Next() error = %v, want active provider stream", err)
	}

	if err := llm.Close(provider); err != nil {
		t.Fatalf("llm.Close error = %v", err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after provider Close error = %T %v, want EOF", err, err)
	}

	_, err = provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Chat() after provider Close error = %T %v, want io.ErrClosedPipe", err, err)
	}
	if calls != 1 {
		t.Fatalf("HTTP calls = %d, want no request after provider Close", calls)
	}
}

func TestGroqLLMChatDefersReferenceRequestUntilNext(t *testing.T) {
	requests := 0
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"id":"chatcmpl-groq","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}` + "\n\n" +
					"data: [DONE]\n\n")),
			Request: r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	if requests != 0 {
		t.Fatalf("requests after Chat = %d, want deferred request", requests)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close before Next error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests after Close before Next = %d, want no provider request", requests)
	}
}

func TestGroqLLMProviderCloseClosesLazyStreamsBeforeRequest(t *testing.T) {
	requests := 0
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     http.StatusText(http.StatusOK),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				`data: {"id":"chatcmpl-groq","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}` + "\n\n" +
					"data: [DONE]\n\n")),
			Request: r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	if err := llm.Close(provider); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if chunk, err := stream.Next(); chunk != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider Close = (%#v, %v), want nil EOF", chunk, err)
	}
	if requests != 0 {
		t.Fatalf("requests after provider Close before Next = %d, want no provider request", requests)
	}
}

func TestGroqLLMProviderCloseCancelsPendingChat(t *testing.T) {
	requests := make(chan *http.Request, 1)
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)
	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("Chat did not start provider request")
	}
	if err := llm.Close(provider); err != nil {
		t.Fatalf("llm.Close error = %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Next after provider Close error = %T %v, want EOF or io.ErrClosedPipe", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next remained blocked after provider Close")
	}
}

func TestGroqLLMChatCallerCancelReturnsContextCanceled(t *testing.T) {
	requests := make(chan *http.Request, 1)
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		requests <- r
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.Chat(
		ctx,
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v, want lazy stream", err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		errCh <- err
	}()

	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("Chat did not start provider request")
	}
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Next canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Next canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next remained blocked after caller cancellation")
	}
}

func TestGroqLLMChatClientClosedStatusReturnsEOF(t *testing.T) {
	client := groqLLMHTTPDoer(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 499,
			Status:     "499 Client Closed Request",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"client closed","type":"client_closed","code":"client_closed"}}`)),
			Request:    r,
		}, nil
	})
	provider := NewGroqLLM("test-key", "llama-3.3-70b-versatile",
		WithGroqLLMBaseURL("https://groq.example/openai/v1"),
		withGroqLLMHTTPClient(client),
	)

	stream, err := provider.Chat(
		context.Background(),
		llm.NewChatContext(),
		llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
	)
	if err != nil {
		t.Fatalf("Chat error = %v, want EOF stream for reference client-closed status", err)
	}
	defer stream.Close()

	if chunk, err := stream.Next(); chunk != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next = (%#v, %v), want nil, io.EOF for reference client-closed status", chunk, err)
	}
}

type groqLLMHTTPDoer func(*http.Request) (*http.Response, error)

func (f groqLLMHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}
