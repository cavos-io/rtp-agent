package respeecher

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRespeecherTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	if provider.baseURL != "https://api.respeecher.com/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "/public/tts/en-rt" {
		t.Fatalf("model = %q, want English public model", provider.model)
	}
	if provider.voiceID != "samantha" {
		t.Fatalf("voice id = %q, want model default voice", provider.voiceID)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true")
	}
}

func TestRespeecherTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.respeecher.com/v1/public/tts/en-rt/tts/bytes" {
		t.Fatalf("url = %q, want bytes endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "test-key" {
		t.Fatalf("X-API-Key = %q, want test key", got)
	}
	if got := req.Header.Get("LiveKit-Plugin-Respeecher-Version"); got != "1.5.15" {
		t.Fatalf("version header = %q, want reference plugin version", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", "hello")
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "samantha")
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", output["sample_rate"])
	}
}

func TestRespeecherTTSOptionsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1/"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
		WithRespeecherTTSVoice("olesia-conversation"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://respeecher.example/v1/public/tts/ua-rt/tts/bytes" {
		t.Fatalf("url = %q, want custom bytes endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "olesia-conversation")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}
}

func TestRespeecherTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestRespeecherTTSStreamURLUsesReferenceQueryAuth(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1"),
	)

	streamURL := buildRespeecherTTSStreamURL(provider)
	if !strings.HasPrefix(streamURL, "wss://respeecher.example/v1/public/tts/en-rt/tts/websocket?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL)
	}
	if !strings.Contains(streamURL, "api_key=test-key") {
		t.Fatalf("stream URL = %q, want query API key", streamURL)
	}
	if !strings.Contains(streamURL, "source=LiveKit-Plugin-Respeecher-Version") {
		t.Fatalf("stream URL = %q, want plugin source query", streamURL)
	}
	if !strings.Contains(streamURL, "version=1.5.15") {
		t.Fatalf("stream URL = %q, want plugin version query", streamURL)
	}
}

func TestRespeecherTTSStreamSendsReferencePayloadAndFinalRequest(t *testing.T) {
	payloadCh := make(chan map[string]any, 2)
	errCh := make(chan error, 1)
	server := newRespeecherTTSTestWebsocketServer(t, func(conn *websocket.Conn, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "test-key" {
			t.Errorf("api_key = %q, want test-key", got)
		}
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				errCh <- err
				return
			}
			payloadCh <- message
		}
	})
	defer server.Close()

	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL(httpToWS(server.URL)),
		WithRespeecherTTSVoice("custom-voice"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("push text: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	first := readRespeecherTestChan(t, payloadCh, errCh)
	if first["context_id"] == "" {
		t.Fatalf("context_id = %#v, want generated id", first["context_id"])
	}
	assertRespeecherPayload(t, first, "transcript", "hello")
	if first["continue"] != true {
		t.Fatalf("continue = %#v, want true", first["continue"])
	}
	voice := first["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "custom-voice")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := first["output_format"].(map[string]any)
	if output["sample_rate"] != float64(48000) || output["encoding"] != "pcm_s16le" {
		t.Fatalf("output_format = %#v, want configured PCM output", output)
	}

	final := readRespeecherTestChan(t, payloadCh, errCh)
	if final["context_id"] != first["context_id"] {
		t.Fatalf("final context_id = %#v, want same context", final["context_id"])
	}
	if final["continue"] != false {
		t.Fatalf("final continue = %#v, want false", final["continue"])
	}
	assertRespeecherPayload(t, final, "transcript", " ")
}

func TestRespeecherTTSStreamDecodesChunkMessages(t *testing.T) {
	server := newRespeecherTTSTestWebsocketServer(t, func(conn *websocket.Conn, r *http.Request) {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read synthesis payload: %v", err)
			return
		}
		var request map[string]any
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		contextID := request["context_id"].(string)
		audio := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04})
		if err := conn.WriteJSON(map[string]any{"context_id": contextID, "type": "chunk", "data": audio}); err != nil {
			t.Errorf("write chunk: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{"context_id": contextID, "type": "done"}); err != nil {
			t.Errorf("write done: %v", err)
			return
		}
	})
	defer server.Close()

	provider := NewRespeecherTTS("test-key", "", WithRespeecherTTSBaseURL(httpToWS(server.URL)))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()
	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("push text: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if string(audio.Frame.Data) != "\x01\x02\x03\x04" {
		t.Fatalf("audio data = %#v, want decoded chunk", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want 24 kHz mono PCM", audio.Frame)
	}
	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second next err = %v, want EOF", err)
	}
}

func assertRespeecherPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func newRespeecherTTSTestWebsocketServer(t *testing.T, handler func(*websocket.Conn, *http.Request)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn, r)
	}))
}

func httpToWS(rawURL string) string {
	return "ws" + strings.TrimPrefix(rawURL, "http")
}

func readRespeecherTestChan[T any](t *testing.T, ch <-chan T, errCh <-chan error) T {
	t.Helper()
	var zero T
	select {
	case got := <-ch:
		return got
	case err := <-errCh:
		t.Fatalf("websocket server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket server")
	}
	return zero
}
