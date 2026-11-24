package elevenlabs

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

// PRD call_logs 3295518 created two replies for one caller turn; only the second was spoken.
// A reply whose audio nobody consumes must not stall its sibling on the shared websocket.
func TestElevenLabsTTSUnconsumedSharedStreamDoesNotStallSibling(t *testing.T) {
	t.Skip("known defect, not fixed here: readLoop blocks on a full stream buffer and stalls sibling contexts; remove this skip to reproduce")
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	go func() {
		reader := bufio.NewReader(serverConn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		if _, err := fmt.Fprintf(serverConn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", elevenLabsTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
			return
		}
		for { // drain client frames
			if _, err := readElevenLabsClientWebsocketJSONFrame(reader); err != nil {
				return
			}
		}
	}()

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil },
		Proxy:          nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider, err := NewElevenLabsTTS("test-key", "voice-1", "eleven_turbo_v2_5",
		WithElevenLabsBaseURL("ws://eleven.test/v1"),
		WithElevenLabsEncoding("pcm_16000"),
	)
	if err != nil {
		t.Fatalf("NewElevenLabsTTS() error = %v", err)
	}
	open := func(text string) *elevenLabsStream {
		s, err := provider.Stream(context.Background())
		if err != nil {
			t.Fatalf("Stream error = %v", err)
		}
		if err := s.PushText(text); err != nil {
			t.Fatalf("PushText error = %v", err)
		}
		if err := s.Flush(); err != nil {
			t.Fatalf("Flush error = %v", err)
		}
		return s.(*elevenLabsStream)
	}
	abandoned := open("alpha beta") // precomputed reply nobody plays
	spoken := open("bravo charlie")
	defer abandoned.Close()
	defer spoken.Close()

	audio := base64.StdEncoding.EncodeToString([]byte{1, 0, 2, 0})
	go func() {
		// More chunks than the abandoned stream's buffer holds, then one for its sibling.
		for i := 0; i < cap(abandoned.audio)+20; i++ {
			if err := writeElevenLabsServerWebsocketJSONFrame(serverConn, map[string]any{"context_id": abandoned.contextID, "audio": audio}); err != nil {
				return
			}
		}
		_ = writeElevenLabsServerWebsocketJSONFrame(serverConn, map[string]any{"context_id": spoken.contextID, "audio": audio})
	}()

	got := make(chan error, 1)
	go func() {
		var a *tts.SynthesizedAudio
		a, err := spoken.Next()
		if err == nil && (a == nil || a.Frame == nil) {
			err = errors.New("no audio frame")
		}
		got <- err
	}()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("sibling Next error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sibling stream got no audio: the shared read loop is blocked on the unconsumed stream")
	}
}
