package elevenlabs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/cavos-io/rtp-agent/library/utils/testutils"
	"github.com/gorilla/websocket"
)

func TestElevenLabsTTS_Synthesize(t *testing.T) {
	// 1. Setup mock REST server returning raw PCM
	pcmData := []byte{0x01, 0x02, 0x03, 0x04}
	server := testutils.NewJSONMockServer(string(pcmData), 200)
	defer server.Close()

	// 2. Initialize adapter
	adapter, _ := NewElevenLabsTTS("test-key", "voice-1", "model-1", WithBaseURL(server.URL))

	// 3. Run Synthesize
	stream, err := adapter.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	// 4. Verify
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if len(chunk.Frame.Data) != 4 {
		t.Errorf("expected 4 bytes of audio, got %d", len(chunk.Frame.Data))
	}
}

func TestElevenLabsTTS_Stream(t *testing.T) {
	// 1. Setup mock WebSocket server
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		for {
			// Read JSON message
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			var req map[string]interface{}
			json.Unmarshal(msg, &req)

			if text, ok := req["text"].(string); ok && text != "" && text != " " {
				// Send back audio
				audioB64 := base64.StdEncoding.EncodeToString([]byte{0xAA, 0xBB})
				resp := elWSResponse{
					Audio:   audioB64,
					IsFinal: true,
				}
				b, _ := json.Marshal(resp)
				conn.WriteMessage(websocket.TextMessage, b)
			}
		}
	})
	defer server.Close()

	// 2. Initialize adapter with mock WS URL
	wsURL := testutils.GetWSURL(server.URL)
	adapter, _ := NewElevenLabsTTS("test-key", "voice-1", "model-1", WithWSURL(wsURL))

	// 3. Start stream
	stream, err := adapter.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	// 4. Push text
	err = stream.PushText("hello")
	if err != nil {
		t.Fatalf("PushText failed: %v", err)
	}

	// 5. Receive audio
	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	// 6. Verify
	if len(audio.Frame.Data) != 2 {
		t.Errorf("expected 2 bytes of audio, got %d", len(audio.Frame.Data))
	}
}
