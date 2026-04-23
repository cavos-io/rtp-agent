package upliftai

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestUpliftAILLM_Chat(t *testing.T) {
	mockResponseChunks := []string{
		`{"id": "up-123", "choices": [{"delta": {"role": "assistant", "content": "UpliftAI!"}}]}`,
	}
	server := testutils.NewSSEMockServer(mockResponseChunks, true)
	defer server.Close()

	l := NewUpliftAILLM("fake-key", "default", 
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

	if chunk.Delta.Content != "UpliftAI!" {
		t.Errorf("Expected content 'UpliftAI!', got '%s'", chunk.Delta.Content)
	}
}
