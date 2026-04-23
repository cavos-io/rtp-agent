package fal

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestFalLLM_Chat(t *testing.T) {
	mockResponse := `{"choices": [{"message": {"role": "assistant", "content": "Fal!"}}]}`
	server := testutils.NewJSONMockServer(mockResponse, http.StatusOK)
	defer server.Close()

	l := NewFalLLM("fake-key", "model-1", 
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

	if chunk.Delta.Content != "Fal!" {
		t.Errorf("Expected content 'Fal!', got '%s'", chunk.Delta.Content)
	}
}
