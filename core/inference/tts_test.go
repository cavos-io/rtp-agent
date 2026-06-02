package inference

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/tokenize"
	"github.com/gorilla/websocket"
)

func TestNewTTSUsesConfiguredSentenceTokenizer(t *testing.T) {
	tokenizer := &recordingSentenceTokenizer{}

	provider := NewTTS("cartesia/sonic-3", "key", "secret", WithSentenceTokenizer(tokenizer))

	if got := provider.sentenceTokenizer; got != tokenizer {
		t.Fatalf("sentenceTokenizer = %T, want configured tokenizer", got)
	}
}

func TestTTSPrewarmReusesConnectionForNextStream(t *testing.T) {
	var connCount atomic.Int32
	sessionCreated := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tts" {
			t.Errorf("path = %q, want /v1/tts", r.URL.Path)
		}
		if got := r.URL.Query().Get("model"); got != "cartesia/sonic-3" {
			t.Errorf("model query = %q, want cartesia/sonic-3", got)
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		connCount.Add(1)

		go func() {
			defer conn.Close()
			for {
				var msg map[string]any
				if err := conn.ReadJSON(&msg); err != nil {
					return
				}
				if msg["type"] == "session.create" {
					select {
					case sessionCreated <- struct{}{}:
					default:
					}
				}
			}
		}()
	}))
	defer server.Close()

	provider := NewTTS("cartesia/sonic-3:voice-id", "key", "secret")
	provider.baseURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1"

	provider.Prewarm()
	select {
	case <-sessionCreated:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prewarmed session.create")
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	if got := connCount.Load(); got != 1 {
		t.Fatalf("connections = %d, want 1 prewarmed connection reused by Stream", got)
	}
}

type recordingSentenceTokenizer struct{}

func (r *recordingSentenceTokenizer) Tokenize(text string, language string) []string {
	return []string{"custom"}
}

func (r *recordingSentenceTokenizer) Stream(language string) tokenize.SentenceStream {
	return tokenize.NewBufferedTokenStream(func(string) []string {
		return []string{"custom"}
	}, 1, 1)
}
