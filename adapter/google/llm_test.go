package google

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestGoogleLLM_ChatStreaming(t *testing.T) {
	// Google Gemini response format is a JSON array of responses or SSE
	// For simplicity in this mock, we'll try to match what genai library expects if it's using REST
	chunks := []string{
		`{"candidates": [{"content": {"parts": [{"text": "Hello"}]}}], "usageMetadata": {"promptTokenCount": 10}}`,
		`{"candidates": [{"content": {"parts": [{"text": " world"}]}}], "usageMetadata": {"candidatesTokenCount": 5}}`,
	}
	server := testutils.NewSSEMockServer(chunks, false)
	defer server.Close()

	// 2. Initialize adapter with mock Client
	l, _ := NewGoogleLLM("test-key", "gemini-2.0-flash", WithHTTPClient(testutils.NewRewritingClient(server.URL)))

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
