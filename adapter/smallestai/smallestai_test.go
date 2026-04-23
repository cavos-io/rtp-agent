package smallestai

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestSmallestAITTS_Synthesize(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 1024))
	})
	defer server.Close()

	tts := NewSmallestAITTS("apiKey", "voice", WithTTSURL(server.URL), WithTTSHttpClient(server.Client()))
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

func TestSmallestAILLM_Initialization(t *testing.T) {
	l := NewSmallestAILLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewSmallestAILLM returned nil")
	}
}
