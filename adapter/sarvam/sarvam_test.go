package sarvam

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestSarvamSTT_Recognize(t *testing.T) {
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"transcript": "Hello"}`))
	})
	defer server.Close()

	s := NewSarvamSTT("apiKey", WithSTTURL(server.URL), WithSTTHttpClient(server.Client()))
	frames := []*model.AudioFrame{{Data: []byte{0x00}}}
	
	event, err := s.Recognize(context.Background(), frames, "en-IN")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if event.Alternatives[0].Text != "Hello" {
		t.Errorf("Expected Hello, got %s", event.Alternatives[0].Text)
	}
}

func TestSarvamTTS_Synthesize(t *testing.T) {
	audioData := base64.StdEncoding.EncodeToString([]byte{0x00, 0x01})
	server := testutils.NewMockServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"audios": ["` + audioData + `"]}`))
	})
	defer server.Close()

	tts := NewSarvamTTS("apiKey", "meera", WithTTSURL(server.URL), WithTTSHttpClient(server.Client()))
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(audio.Frame.Data) != 2 {
		t.Errorf("Expected 2 bytes, got %d", len(audio.Frame.Data))
	}
}
