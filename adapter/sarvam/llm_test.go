package sarvam

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestSarvamLLMDefaultsMatchReference(t *testing.T) {
	provider, err := NewSarvamLLMWithError("test-key", "")
	if err != nil {
		t.Fatalf("new sarvam llm: %v", err)
	}

	if provider.Model() != "sarvam-30b" {
		t.Fatalf("model = %q, want reference default", provider.Model())
	}
	if provider.Provider() != "Sarvam" {
		t.Fatalf("provider = %q, want Sarvam", provider.Provider())
	}
	if provider.BaseURL() != "https://api.sarvam.ai/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.BaseURL())
	}
}

func TestNewSarvamLLMUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SARVAM_API_KEY", "env-key")

	provider, err := NewSarvamLLMWithError("", "")
	if err != nil {
		t.Fatalf("new sarvam llm: %v", err)
	}
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit, err := NewSarvamLLMWithError("explicit-key", "")
	if err != nil {
		t.Fatalf("new explicit sarvam llm: %v", err)
	}
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewSarvamLLMRequiresAPIKey(t *testing.T) {
	t.Setenv("SARVAM_API_KEY", "")

	_, err := NewSarvamLLMWithError("", "")
	if err == nil {
		t.Fatal("NewSarvamLLMWithError returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SARVAM_API_KEY") {
		t.Fatalf("NewSarvamLLMWithError error = %q, want SARVAM_API_KEY guidance", err)
	}
}

func TestSarvamLLMRejectsUnsupportedModel(t *testing.T) {
	_, err := NewSarvamLLMWithError("test-key", "not-sarvam")
	if err == nil {
		t.Fatal("NewSarvamLLMWithError returned nil error, want unsupported model error")
	}
	if !strings.Contains(err.Error(), "unsupported Sarvam model") {
		t.Fatalf("error = %v, want unsupported model error", err)
	}
}

func TestBuildSarvamLLMChatRequestMatchesReferenceHeadersAndBody(t *testing.T) {
	provider := NewSarvamLLM("test-key", "sarvam-m",
		WithSarvamLLMBaseURL("https://sarvam.example/v1"),
		WithSarvamLLMExtraHeaders(map[string]string{"X-Custom": "kept", "api-subscription-key": "override"}),
		WithSarvamLLMExtraBody(map[string]any{
			"max_tokens":     64,
			"wiki_grounding": true,
			"unsupported":    "drop",
		}),
	)
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		ID:   "msg-1",
		Role: llm.ChatRoleUser,
		Content: []llm.ChatContent{{
			Text: "hello",
		}},
	})

	req, err := buildSarvamLLMChatRequest(context.Background(), provider, chatCtx, &llm.ChatOptions{
		ExtraParams: map[string]any{
			"seed":        123,
			"temperature": 0.7,
			"drop_me":     true,
		},
	})
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://sarvam.example/v1/chat/completions" {
		t.Fatalf("URL = %q, want chat completions endpoint", req.URL.String())
	}
	if req.Header.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want enforced api key", req.Header.Get("api-subscription-key"))
	}
	if req.Header.Get("User-Agent") == "" {
		t.Fatal("User-Agent missing")
	}
	if req.Header.Get("X-Custom") != "kept" {
		t.Fatalf("X-Custom = %q, want extra header", req.Header.Get("X-Custom"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["model"] != "sarvam-m" || payload["stream"] != true {
		t.Fatalf("payload = %+v, want model and stream", payload)
	}
	messages := payload["messages"].([]any)
	first := messages[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hello" {
		t.Fatalf("message = %+v, want user text", first)
	}
	if payload["max_tokens"] != float64(64) || payload["wiki_grounding"] != true || payload["seed"] != float64(123) {
		t.Fatalf("payload = %+v, want allowed extra body params", payload)
	}
	if _, ok := payload["unsupported"]; ok {
		t.Fatalf("unsupported extra body was not filtered: %+v", payload)
	}
	if _, ok := payload["drop_me"]; ok {
		t.Fatalf("unsupported chat extra param was not filtered: %+v", payload)
	}
	if _, ok := payload["temperature"]; ok {
		t.Fatalf("temperature should not be passed through Sarvam extra body filter: %+v", payload)
	}
}

func TestSarvamLLMChatStreamsOpenAICompatibleContent(t *testing.T) {
	client := newSarvamTestHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-subscription-key") != "test-key" {
			t.Fatalf("api-subscription-key = %q, want test-key", r.Header.Get("api-subscription-key"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	provider := NewSarvamLLM("test-key", "",
		WithSarvamLLMBaseURL("https://sarvam.test/v1"),
		withSarvamLLMHTTPClient(client),
	)
	stream, err := provider.Chat(context.Background(), llm.NewChatContext())
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	defer stream.Close()

	first, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	second, err := stream.Next()
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	if got := first.Delta.Content + second.Delta.Content; got != "hello" {
		t.Fatalf("streamed content = %q, want hello", got)
	}
}

func TestSarvamLLMImplementsInterface(t *testing.T) {
	var _ llm.LLM = NewSarvamLLM("test-key", "")
}

func newSarvamTestHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: sarvamRoundTripper(func(req *http.Request) (*http.Response, error) {
			rec := httptestResponseRecorder{
				header: make(http.Header),
				code:   http.StatusOK,
			}
			handler.ServeHTTP(&rec, req)
			return &http.Response{
				StatusCode: rec.code,
				Header:     rec.header,
				Body:       io.NopCloser(strings.NewReader(rec.body.String())),
				Request:    req,
			}, nil
		}),
	}
}

type sarvamRoundTripper func(*http.Request) (*http.Response, error)

func (f sarvamRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type httptestResponseRecorder struct {
	header http.Header
	body   strings.Builder
	code   int
}

func (r *httptestResponseRecorder) Header() http.Header {
	return r.header
}

func (r *httptestResponseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *httptestResponseRecorder) WriteHeader(statusCode int) {
	r.code = statusCode
}
