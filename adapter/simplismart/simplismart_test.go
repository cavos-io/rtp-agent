package simplismart

import (
	"context"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestSimplismartSTT_Recognize(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text": "Hello"}`))
	})
	defer server.Close()

	s := NewSimplismartSTT("apiKey", WithSTTURL(server.URL), WithSTTHttpClient(server.Client()))
	frames := []*model.AudioFrame{{Data: []byte{0x00}}}
	
	event, err := s.Recognize(context.Background(), frames, "en")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if event.Alternatives[0].Text != "Hello" {
		t.Errorf("Expected Hello, got %s", event.Alternatives[0].Text)
	}
}

func TestSimplismartTTS_Synthesize(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 1024))
	})
	defer server.Close()

	tts := NewSimplismartTTS("apiKey", "voice", WithTTSURL(server.URL), WithTTSHttpClient(server.Client()))
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

func TestSimplismartLLM_Initialization(t *testing.T) {
	l := NewSimplismartLLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewSimplismartLLM returned nil")
	}
}
