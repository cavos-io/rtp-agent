package fireworksai

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestFireworksLLM_Chat(t *testing.T) {
	mockResponseChunks := []string{
		`{"id": "fw-123", "choices": [{"delta": {"role": "assistant", "content": "Fireworks!"}}]}`,
	}
	server := testutils.NewSSEMockServer(mockResponseChunks, true)
	defer server.Close()

	l := NewFireworksLLM("fake-key", "firefunction", 
		WithBaseURL(server.URL),
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

	if chunk.Delta.Content != "Fireworks!" {
		t.Errorf("Expected content 'Fireworks!', got '%s'", chunk.Delta.Content)
	}
}
