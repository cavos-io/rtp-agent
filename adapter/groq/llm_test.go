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

	_, _ = provider.Chat(context.Background(), llm.NewChatContext(), llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}))

	if !strings.Contains(requestBody, `"parallel_tool_calls":false`) {
		t.Fatalf("request body = %s, want provider parallel_tool_calls false", requestBody)
	}
	if !strings.Contains(requestBody, `"tool_choice":"none"`) {
		t.Fatalf("request body = %s, want provider tool_choice none", requestBody)
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
	errCh := make(chan error, 1)
	go func() {
		stream, err := provider.Chat(
			context.Background(),
			llm.NewChatContext(),
			llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
		)
		if stream != nil {
			_ = stream.Close()
		}
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
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Chat after provider Close error = %T %v, want io.ErrClosedPipe", err, err)
		}
	case <-time.After(time.Second):
		t.Fatal("Chat remained blocked after provider Close")
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
	errCh := make(chan error, 1)
	go func() {
		stream, err := provider.Chat(
			ctx,
			llm.NewChatContext(),
			llm.WithConnectOptions(llm.APIConnectOptions{MaxRetry: 0}),
		)
		if stream != nil {
			_ = stream.Close()
		}
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
			t.Fatalf("Chat canceled error = %T %v, want context.Canceled", err, err)
		}
		var connectionErr *llm.APIConnectionError
		if errors.As(err, &connectionErr) {
			t.Fatalf("Chat canceled error = %T, want raw context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Chat remained blocked after caller cancellation")
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
