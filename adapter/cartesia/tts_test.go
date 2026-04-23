package cartesia

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/cavos-io/rtp-agent/library/utils/testutils"
)

func TestCartesiaTTS_Synthesize(t *testing.T) {
	mockAudio := []byte{0x01, 0x02, 0x03, 0x04}
	server := testutils.NewJSONMockServer(string(mockAudio), http.StatusOK)
	defer server.Close()

	tts := NewCartesiaTTS("fake-key", "voice-1", "sonic",
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	stream, err := tts.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(chunk.Frame.Data) != 4 {
		t.Errorf("Expected 4 bytes of audio, got %d", len(chunk.Frame.Data))
	}
}

func TestCartesiaTTS_Stream(t *testing.T) {
	mockAudio := []byte{0x01, 0x02, 0x03, 0x04}
	encodedAudio := base64.StdEncoding.EncodeToString(mockAudio)
	
	server := testutils.NewWebSocketMockServer(func(conn *websocket.Conn) {
		// Expect init msg
		_, _, _ = conn.ReadMessage()
		
		// Send chunk
		resp := fmt.Sprintf(`{"type": "chunk", "data": "%s", "done": false}`, encodedAudio)
		_ = conn.WriteMessage(1, []byte(resp))
		
		// Send done
		_ = conn.WriteMessage(1, []byte(`{"type": "done"}`))
	})
	defer server.Close()

	tts := NewCartesiaTTS("fake-key", "voice-1", "sonic",
		WithWSURL(server.URL),
	)

	stream, err := tts.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(audio.Frame.Data) != 4 {
		t.Errorf("Expected 4 bytes, got %d", len(audio.Frame.Data))
	}
}
