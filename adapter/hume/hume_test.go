package hume

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestHumeTTS_Synthesize(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 1024))
	})
	defer server.Close()

	tts := NewHumeTTS("apiKey", "model", WithTTSURL(server.URL), WithTTSHttpClient(server.Client()))
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(audio.Frame.Data) != 1024 {
		t.Errorf("Expected 1024 bytes, got %d", len(audio.Frame.Data))
	}
}

func TestHumeLLM_Chat(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "Hello there"
				}
			}]
		}`))
	})
	defer server.Close()

	l := NewHumeLLM("apiKey", "model", WithLLMURL(server.URL), WithLLMHttpClient(server.Client()))
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "Hi"}}})

	stream, err := l.Chat(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if chunk.Delta.Content != "Hello there" {
		t.Errorf("Expected 'Hello there', got %s", chunk.Delta.Content)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("Expected EOF, got %v", err)
	}
}
