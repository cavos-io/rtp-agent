package xai

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestXaiLLM_Chat(t *testing.T) {
	mockResponseChunks := []string{
		`{"id": "chatcmpl-123", "choices": [{"delta": {"role": "assistant", "content": "Hello!"}}]}`,
	}
	server := testutils.NewSSEMockServer(mockResponseChunks, true)
	defer server.Close()

	l := NewXaiLLM("fake-key", "grok-2", 
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	chatCtx := &llm.ChatContext{
		Items: []llm.ChatItem{
			&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Hi"}}},
		},
	}

	stream, err := l.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if chunk.Delta.Content != "Hello!" {
		t.Errorf("Expected content 'Hello!', got '%s'", chunk.Delta.Content)
	}

	_, err = stream.Next()
	if err == nil || err.Error() != "EOF" {
		t.Errorf("Expected EOF, got %v", err)
	}
}
