package clova

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
)

func TestClovaSTT_Recognize(t *testing.T) {
	server := testutils.NewJSONMockServer(`{"text": "안녕하세요"}`, 200)
	defer server.Close()

	s := NewClovaSTT("id", "secret", WithSTTBaseURL(server.URL))
	ev, err := s.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("fake-audio")}}, "Kor")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	if ev.Alternatives[0].Text != "안녕하세요" {
		t.Errorf("expected '안녕하세요', got '%s'", ev.Alternatives[0].Text)
	}
}

func TestClovaTTS_Synthesize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fake-audio-stream"))
	}))
	defer server.Close()

	ts := NewClovaTTS("id", "secret", "nara", WithTTSBaseURL(server.URL))
	stream, err := ts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if string(chunk.Frame.Data) != "fake-audio-stream" {
		t.Errorf("expected 'fake-audio-stream', got '%s'", string(chunk.Frame.Data))
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}
