package assemblyai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

func TestAssemblyAISTT_Recognize(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"upload_url": "http://mock/audio"}`))
	})
	mux.HandleFunc("/transcript", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": "test_job_123"}`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	s := NewAssemblyAISTT("test-key", WithSTTBaseURL(server.URL))
	ev, err := s.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("fake-audio")}}, "en-US")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	expected := "[AssemblyAI Job ID: test_job_123]"
	if ev.Alternatives[0].Text != expected {
		t.Errorf("expected %s, got %s", expected, ev.Alternatives[0].Text)
	}
}

func TestAssemblyAISTT_Stream(t *testing.T) {
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		// Read audio data
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		// Send back transcript
		resp := map[string]interface{}{
			"message_type": "FinalTranscript",
			"text":         "Hello AssemblyAI",
			"confidence":   0.95,
		}
		conn.WriteJSON(resp)
	})
	defer server.Close()

	s := NewAssemblyAISTT("test-key", WithWSURL(testutils.GetWSURL(server.URL)))
	stream, err := s.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	err = stream.PushFrame(&model.AudioFrame{Data: []byte("fake-audio")})
	if err != nil {
		t.Fatalf("PushFrame failed: %v", err)
	}

	ev, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if ev.Alternatives[0].Text != "Hello AssemblyAI" {
		t.Errorf("expected 'Hello AssemblyAI', got '%s'", ev.Alternatives[0].Text)
	}
}
