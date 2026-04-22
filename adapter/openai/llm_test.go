package openai

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestOpenAILLM_ChatStreaming(t *testing.T) {
	// 1. Setup mock SSE chunks
	chunks := []string{
		`{"id":"chat-123","choices":[{"delta":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`{"id":"chat-123","choices":[{"delta":{"content":" world"}}]}`,
	}
	server := testutils.NewSSEMockServer(chunks)
	defer server.Close()

	// 2. Initialize adapter with mock URL
	l := NewOpenAILLM("test-key", "gpt-4o", WithBaseURL(server.URL))

	// 3. Run chat
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Hi"}}})

	stream, err := l.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("failed to start chat: %v", err)
	}
	defer stream.Close()

	// 4. Collect chunks
	var fullText string
	var lastID string
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		fullText += chunk.Delta.Content
		lastID = chunk.ID
	}

	// 5. Verify results
	if fullText != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", fullText)
	}
	if lastID != "chat-123" {
		t.Errorf("expected ID 'chat-123', got '%s'", lastID)
	}
}

func TestOpenAILLM_Options(t *testing.T) {
	l := NewOpenAILLM("key", "model", WithBaseURL("http://localhost:1234"))
	if l.model != "model" {
		t.Errorf("expected model 'model', got '%s'", l.model)
	}
}
