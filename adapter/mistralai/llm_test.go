package mistralai

import (
	"context"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestMistralLLM_ChatStreaming(t *testing.T) {
	// 1. Setup mock SSE chunks
	chunks := []string{
		`{"id":"1","choices":[{"delta":{"content":"Mistral"},"index":0}]}`,
		`{"id":"1","choices":[{"delta":{"content":" is smart"},"index":0}]}`,
	}
	server := testutils.NewSSEMockServer(chunks, true)
	defer server.Close()

	// 2. Initialize adapter with mock URL
	l := NewMistralLLM("test-key", "mistral-tiny", openai.WithBaseURL(server.URL))

	// 3. Run chat
	stream, err := l.Chat(context.Background(), &llm.ChatContext{})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	defer stream.Close()

	// 4. Collect results
	var content string
	for {
		chunk, err := stream.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		content += chunk.Delta.Content
	}

	if content != "Mistral is smart" {
		t.Errorf("expected 'Mistral is smart', got '%s'", content)
	}
}
