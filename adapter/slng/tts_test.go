package slng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestTTSConstructorContract(t *testing.T) {
	var _ coretts.TTS = (*TTS)(nil)
	provider := NewTTS("key", WithTTSModel("custom/model"), WithTTSVoice("voice"))
	if provider.apiKey != "key" || provider.model != "custom/model" || provider.voice != "voice" {
		t.Fatalf("NewTTS() options not applied: key=%q model=%q voice=%q", provider.apiKey, provider.model, provider.voice)
	}
}

func TestTTSConstructorPreservesOptionValidationErrors(t *testing.T) {
	connection := TTSConnectionConfig{
		Endpoint: "wss://api.slng.ai/v1/bridges/unmute/tts/deepgram/aura:2",
	}
	for _, test := range []struct {
		name string
		opts []TTSOption
		want string
	}{
		{
			name: "invalid model",
			opts: []TTSOption{WithTTSModel("invalid")},
			want: slngModelIdentifierError,
		},
		{
			name: "invalid base URL",
			opts: []TTSOption{WithTTSBaseURL("://invalid")},
			want: `invalid bridge base URL "://invalid"`,
		},
		{
			name: "invalid connection endpoint",
			opts: []TTSOption{WithTTSConnections(TTSConnectionConfig{Endpoint: "wss://api.slng.ai/wrong"})},
			want: "TTS endpoint must target the Unmute Bridge path /v1/bridges/unmute/tts/",
		},
		{
			name: "model then connections",
			opts: []TTSOption{WithTTSModel("deepgram/aura:2"), WithTTSConnections(connection)},
			want: "use model or connections, not both",
		},
		{
			name: "connections then model",
			opts: []TTSOption{WithTTSConnections(connection), WithTTSModel("deepgram/aura:2")},
			want: "use model or connections, not both",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := NewTTS("key", test.opts...)
			if provider.optionError == nil || provider.optionError.Error() != test.want {
				t.Fatalf("option error = %v, want %q", provider.optionError, test.want)
			}
		})
	}
}

func TestTTSSynthesizeSendsFullInputOnceAcrossChunkModes(t *testing.T) {
	const input = "Hello world. More text"
	for _, test := range []struct {
		name string
		opts []TTSOption
	}{
		{name: "default word"},
		{name: "word", opts: []TTSOption{WithTTSTextChunking(TTSChunkingWord, 5)}},
		{name: "auto", opts: []TTSOption{WithTTSTextChunking(TTSChunkingAuto, 5)}},
		{name: "phrase", opts: []TTSOption{WithTTSTextChunking(TTSChunkingPhrase, 5)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			messages := make(chan map[string]any, 8)
			upgrader := websocket.Upgrader{}
			endpoint := newSLNGInMemoryWebsocketEndpoints(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade websocket: %v", err)
					return
				}
				defer conn.Close()
				for {
					_, payload, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var message map[string]any
					if json.Unmarshal(payload, &message) != nil {
						continue
					}
					messages <- message
					if message["type"] == "flush" {
						return
					}
				}
			}))[0]

			opts := append([]TTSOption{WithTTSEndpoint(endpoint)}, test.opts...)
			provider := NewTTS("test-key", opts...)
			defer provider.Close()
			stream, err := provider.Synthesize(context.Background(), input)
			if err != nil {
				t.Fatalf("Synthesize() error = %v", err)
			}
			defer stream.Close()

			var got []string
			for {
				select {
				case message := <-messages:
					switch message["type"] {
					case "text":
						got = append(got, slngString(message["text"]))
					case "flush":
						if want := []string{input}; !reflect.DeepEqual(got, want) {
							t.Fatalf("text frames = %#v, want %#v", got, want)
						}
						return
					}
				case <-time.After(time.Second):
					t.Fatal("timed out waiting for chunked synthesis messages")
				}
			}
		})
	}
}

func TestTTSStreamReturnsTerminalAudioBeforeEOF(t *testing.T) {
	for _, payload := range []string{
		`{"audio":"AQI=","isFinal":true}`,
		`{"type":"audio_end","data":"AQI="}`,
		`{"type":"event","data":{"event_type":"final","audio":"AQI="}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			endpoint := newSLNGInMemoryWebsocketEndpoints(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade websocket: %v", err)
					return
				}
				defer conn.Close()
				for {
					_, input, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var message map[string]any
					if json.Unmarshal(input, &message) == nil && message["type"] == "flush" {
						_ = conn.WriteMessage(websocket.TextMessage, []byte(payload))
						return
					}
				}
			}))[0]

			provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
			defer provider.Close()
			stream, err := provider.Stream(context.Background())
			if err != nil {
				t.Fatalf("Stream() error = %v", err)
			}
			if err := stream.PushText("hello"); err != nil {
				t.Fatalf("PushText() error = %v", err)
			}
			if err := stream.Flush(); err != nil {
				t.Fatalf("Flush() error = %v", err)
			}

			audio, err := stream.Next()
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			if audio == nil || audio.Frame == nil || !audio.IsFinal || !reflect.DeepEqual(audio.Frame.Data, []byte{1, 2}) {
				t.Fatalf("terminal audio = %#v, want final bytes [1 2]", audio)
			}
			if _, err := stream.Next(); !errors.Is(err, io.EOF) {
				t.Fatalf("Next() after terminal audio error = %v, want io.EOF", err)
			}
		})
	}
}

func TestTTSStreamReturnsCompletionBoundaryAfterAudio(t *testing.T) {
	upgrader := websocket.Upgrader{}
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, input, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(input, &message) == nil && message["type"] == "flush" {
				_ = conn.WriteJSON(map[string]any{"type": "audio_chunk", "data": "AQI="})
				_ = conn.WriteJSON(map[string]any{"type": "done"})
				return
			}
		}
	}))[0]

	provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil || audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next() = (%#v, %v), want non-final audio", audio, err)
	}
	boundary, err := stream.Next()
	if err != nil || boundary == nil || boundary.Frame != nil || !boundary.IsFinal {
		t.Fatalf("second Next() = (%#v, %v), want final boundary", boundary, err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("third Next() error = %v, want io.EOF", err)
	}
}

func TestTTSStreamPreservesPCMSampleBoundariesAcrossMessages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	endpoint := newSLNGInMemoryWebsocketEndpoints(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, input, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(input, &message) != nil || message["type"] != "flush" {
				continue
			}
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte{0x11, 0x22, 0x33})
			_ = conn.WriteJSON(map[string]any{
				"type": "audio_chunk",
				"data": base64.StdEncoding.EncodeToString([]byte{0x44, 0x55, 0x66}),
			})
			_ = conn.WriteJSON(map[string]any{
				"audio":   base64.StdEncoding.EncodeToString([]byte{0x77}),
				"isFinal": true,
			})
			return
		}
	}))[0]

	provider := NewTTS("test-key", WithTTSEndpoint(endpoint))
	defer provider.Close()
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize() error = %v", err)
	}
	defer stream.Close()

	var got []byte
	for {
		audio, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if audio.Frame == nil {
			if !audio.IsFinal {
				t.Fatalf("boundary = %#v, want final marker", audio)
			}
			continue
		}
		if len(audio.Frame.Data)%2 != 0 {
			t.Fatalf("PCM frame has %d bytes, want complete 16-bit samples", len(audio.Frame.Data))
		}
		got = append(got, audio.Frame.Data...)
	}
	if want := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PCM bytes = %v, want %v", got, want)
	}
}

func TestTTSStreamFallsBackWhenCompletionPrecedesAudio(t *testing.T) {
	upgrader := websocket.Upgrader{}
	endpoints := newSLNGInMemoryWebsocketEndpoints(t,
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade primary websocket: %v", err)
				return
			}
			defer conn.Close()
			for {
				_, input, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var message map[string]any
				if json.Unmarshal(input, &message) == nil && message["type"] == "flush" {
					_ = conn.WriteJSON(map[string]any{"type": "done"})
					return
				}
			}
		}),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade fallback websocket: %v", err)
				return
			}
			defer conn.Close()
			for {
				_, input, err := conn.ReadMessage()
				if err != nil {
					return
				}
				var message map[string]any
				if json.Unmarshal(input, &message) == nil && message["type"] == "flush" {
					_ = conn.WriteMessage(websocket.BinaryMessage, []byte{3, 4})
					return
				}
			}
		}),
	)
	provider := NewTTS("test-key", WithTTSConnections(
		TTSConnectionConfig{Endpoint: endpoints[0] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
		TTSConnectionConfig{Endpoint: endpoints[1] + "/v1/bridges/unmute/tts/deepgram/aura:2"},
	))
	defer provider.Close()
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText() error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if audio == nil || audio.Frame == nil || !reflect.DeepEqual(audio.Frame.Data, []byte{3, 4}) {
		t.Fatalf("fallback audio = %#v, want bytes [3 4]", audio)
	}
}
