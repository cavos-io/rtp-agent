package anthropic

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestAnthropicLLM_ChatStreaming(t *testing.T) {
	// 1. Setup mock SSE chunks for Anthropic
	chunks := []string{
		`{"type": "message_start", "message": {"id": "msg_123", "usage": {"input_tokens": 10}}}`,
		`{"type": "content_block_start", "content_block": {"type": "text"}}`,
		`{"type": "content_block_delta", "delta": {"type": "text_delta", "text": "Hello"}}`,
		`{"type": "content_block_delta", "delta": {"type": "text_delta", "text": " world"}}`,
		`{"type": "message_delta", "usage": {"output_tokens": 5}}`,
		`{"type": "message_stop"}`,
	}
	server := testutils.NewSSEMockServer(chunks)
	defer server.Close()

	// 2. Initialize adapter with mock URL
	l, _ := NewAnthropicLLM("test-key", "claude-3-5-sonnet", WithBaseURL(server.URL))

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
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		if chunk.Delta != nil {
			fullText += chunk.Delta.Content
		}
	}

	// 5. Verify results
	if fullText != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", fullText)
	}
}
