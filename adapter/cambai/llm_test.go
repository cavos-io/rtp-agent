package cambai

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestCambaiLLM_Chat(t *testing.T) {
	mockResponse := `{"choices": [{"delta": {"content": "Hello from Cambai"}}]}`
	server := testutils.NewSSEMockServer([]string{mockResponse}, true)
	defer server.Close()

	l := NewCambaiLLM("fake-key", "cambai-model", 
		WithLLMBaseURL(server.URL),
	)

	chatCtx := llm.NewChatContext()
	stream, err := l.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if chunk.Delta.Content != "Hello from Cambai" {
		t.Errorf("Expected 'Hello from Cambai', got '%s'", chunk.Delta.Content)
	}
}
