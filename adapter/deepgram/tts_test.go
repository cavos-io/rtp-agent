package deepgram

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

type mockTransport struct {
	roundTrip func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTrip(req)
}

func TestDeepgramTTS_Synthesize(t *testing.T) {
	mockClient := &http.Client{
		Transport: &mockTransport{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBuffer([]byte("deepgram tts audio"))),
				}, nil
			},
		},
	}

	tts := NewDeepgramTTS("key", "aura", WithTTSHttpClient(mockClient))
	stream, err := tts.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "deepgram tts audio" {
		t.Errorf("Expected 'deepgram tts audio', got %q", string(chunk.Frame.Data))
	}
}

func TestDeepgramTTS_Stream(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			mt, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage {
				if strings.Contains(string(message), "Speak") {
					conn.WriteMessage(websocket.BinaryMessage, []byte("streaming audio"))
				}
				if strings.Contains(string(message), "Close") {
					return
				}
			}
		}
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	
	dialer := &websocket.Dialer{}
	tts := NewDeepgramTTS("key", "aura", WithTTSDialer(dialer), WithTTSWSURL(wsURL))
	
	stream, err := tts.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	_ = stream.PushText("hello")
	
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if string(chunk.Frame.Data) != "streaming audio" {
		t.Errorf("Expected 'streaming audio', got %q", string(chunk.Frame.Data))
	}
}
