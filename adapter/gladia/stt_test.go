package gladia

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

func TestGladiaSTT_Stream(t *testing.T) {
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		// Read init message
		var initMsg map[string]interface{}
		if err := conn.ReadJSON(&initMsg); err != nil {
			return
		}
		// Read audio data
		var audioMsg map[string]interface{}
		if err := conn.ReadJSON(&audioMsg); err != nil {
			return
		}
		// Send back transcript
		resp := map[string]interface{}{
			"type":          "final",
			"transcription": "Hello Gladia",
			"confidence":    0.98,
		}
		conn.WriteJSON(resp)
	})
	defer server.Close()

	s := NewGladiaSTT("test-key", WithSTTBaseURL(testutils.GetWSURL(server.URL)))
	stream, err := s.Stream(context.Background(), "en")
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

	if ev.Alternatives[0].Text != "Hello Gladia" {
		t.Errorf("expected 'Hello Gladia', got '%s'", ev.Alternatives[0].Text)
	}
}
