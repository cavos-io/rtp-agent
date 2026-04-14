package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type requestRecorder struct {
	mu   sync.Mutex
	body []byte
}

func (r *requestRecorder) set(body []byte) {
	r.mu.Lock()
	r.body = append([]byte(nil), body...)
	r.mu.Unlock()
}

func (r *requestRecorder) getJSON(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()

	var payload map[string]any
	if err := json.Unmarshal(r.body, &payload); err != nil {
		t.Fatalf("failed to decode request payload: %v", err)
	}
	return payload
}

func newMockStreamServer(t *testing.T, recorder *requestRecorder) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, req)
			return
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		recorder.set(body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"index\":0}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
}

type testTool struct{}

func (testTool) ID() string          { return "tool-id" }
func (testTool) Name() string        { return "tool-name" }
func (testTool) Description() string { return "tool description" }
func (testTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string"},
		},
	}
}
func (testTool) Execute(ctx context.Context, args map[string]any) (any, error) { return "ok", nil }

func newChatContext() *llm.ChatContext {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "hello"}},
		CreatedAt: time.Now(),
	})
	return chatCtx
}

func TestChatDoesNotSendParallelToolCallsWithoutTools(t *testing.T) {
	recorder := &requestRecorder{}
	server := newMockStreamServer(t, recorder)
	defer server.Close()

	model := NewOpenAILLMWithBaseURL("test-key", "gpt-4o-mini", server.URL+"/v1")
	stream, err := model.Chat(context.Background(), newChatContext())
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	_, _ = stream.Next()
	_ = stream.Close()

	payload := recorder.getJSON(t)
	if _, ok := payload["parallel_tool_calls"]; ok {
		t.Fatalf("parallel_tool_calls must not be present when tools are omitted")
	}
}

func TestChatSendsParallelToolCallsWithTools(t *testing.T) {
	recorder := &requestRecorder{}
	server := newMockStreamServer(t, recorder)
	defer server.Close()

	model := NewOpenAILLMWithBaseURL("test-key", "gpt-4o-mini", server.URL+"/v1")
	stream, err := model.Chat(context.Background(), newChatContext(), llm.WithTools([]llm.Tool{testTool{}}))
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	_, _ = stream.Next()
	_ = stream.Close()

	payload := recorder.getJSON(t)
	value, ok := payload["parallel_tool_calls"]
	if !ok {
		t.Fatalf("parallel_tool_calls must be present when tools are provided")
	}
	flag, ok := value.(bool)
	if !ok || !flag {
		t.Fatalf("expected parallel_tool_calls=true, got: %#v", value)
	}
	if _, ok := payload["tools"]; !ok {
		t.Fatalf("expected tools payload")
	}
}
