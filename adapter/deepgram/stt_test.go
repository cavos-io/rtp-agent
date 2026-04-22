package deepgram

import (
	"context"
	"encoding/json"
	"testing"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/cavos-io/rtp-agent/model"
	"github.com/gorilla/websocket"
)

func TestDeepgramSTT_Recognize(t *testing.T) {
	// 1. Setup mock REST server
	resp := `{"results": {"channels": [{"alternatives": [{"transcript": "hello world", "confidence": 0.99}]}]}}`
	server := testutils.NewJSONMockServer(resp, 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter := NewDeepgramSTT("test-key", "nova-2", WithBaseURL(server.URL))

	// 3. Run Recognize
	frames := []*model.AudioFrame{{Data: []byte{0, 0, 0}}}
	event, err := adapter.Recognize(context.Background(), frames, "en-US")
	if err != nil {
		t.Fatalf("Recognize failed: %v", err)
	}

	// 4. Verify
	if event.Alternatives[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", event.Alternatives[0].Text)
	}
}

func TestDeepgramSTT_Stream(t *testing.T) {
	// 1. Setup mock WebSocket server
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		for {
			// Read audio data (binary)
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
			// Send back a result
			resp := dgResponse{
				Type: "Results",
				IsFinal: true,
			}
			resp.Channel.Alternatives = append(resp.Channel.Alternatives, struct {
				Transcript string  `json:"transcript"`
				Confidence float64 `json:"confidence"`
			}{Transcript: "streaming test", Confidence: 0.95})
			
			b, _ := json.Marshal(resp)
			conn.WriteMessage(websocket.TextMessage, b)
		}
	})
	defer server.Close()

	// 2. Initialize adapter with mock WS URL
	wsURL := testutils.GetWSURL(server.URL)
	adapter := NewDeepgramSTT("test-key", "nova-2", WithWSURL(wsURL))

	// 3. Start stream
	stream, err := adapter.Stream(context.Background(), "en-US")
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	// 4. Push frame
	err = stream.PushFrame(&model.AudioFrame{Data: []byte{1, 2, 3}})
	if err != nil {
		t.Fatalf("PushFrame failed: %v", err)
	}

	// 5. Receive event
	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	// 6. Verify
	if event.Alternatives[0].Text != "streaming test" {
		t.Errorf("expected 'streaming test', got '%s'", event.Alternatives[0].Text)
	}
}
