package baseten

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBasetenTTSDefaultsMatchReferenceOptions(t *testing.T) {
	provider := NewBasetenTTS("test-key", "model-id")

	if provider.modelEndpoint != "https://model-model-id.api.baseten.co/environments/production/predict" {
		t.Fatalf("endpoint = %q, want generated model predict endpoint", provider.modelEndpoint)
	}
	if provider.voice != "tara" {
		t.Fatalf("voice = %q, want tara", provider.voice)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.temperature != 0.6 {
		t.Fatalf("temperature = %.1f, want 0.6", provider.temperature)
	}
	if provider.maxTokens != 2000 {
		t.Fatalf("max tokens = %d, want 2000", provider.maxTokens)
	}
	if provider.bufferSize != 10 {
		t.Fatalf("buffer size = %d, want 10", provider.bufferSize)
	}
	if provider.Capabilities().Streaming {
		t.Fatalf("streaming = true for https endpoint, want false")
	}
}

func TestBasetenTTSWebSocketEndpointReportsStreamingCapability(t *testing.T) {
	provider := NewBasetenTTS("test-key", "",
		WithBasetenTTSModelEndpoint("wss://model.example/websocket"),
	)

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false for websocket endpoint, want true")
	}
}

func TestBuildBasetenTTSRequestMatchesReferencePayload(t *testing.T) {
	provider := NewBasetenTTS("test-key", "",
		WithBasetenTTSModelEndpoint("https://model.example/predict"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSLanguage("es"),
		WithBasetenTTSTemperature(0.8),
	)

	req, err := buildBasetenTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://model.example/predict" {
		t.Fatalf("URL = %q, want configured endpoint", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Api-Key test-key" {
		t.Fatalf("Authorization = %q, want Api-Key header", req.Header.Get("Authorization"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertBasetenPayload(t, payload, "prompt", "hello")
	assertBasetenPayload(t, payload, "voice", "emma")
	assertBasetenPayload(t, payload, "language", "es")
	assertBasetenPayload(t, payload, "temperature", float64(0.8))
	if _, ok := payload["text"]; ok {
		t.Fatalf("payload still uses legacy text field: %+v", payload)
	}
}

func TestBasetenTTSChunkedStreamReturnsRawAudioChunks(t *testing.T) {
	stream := &basetenTTSChunkedStream{
		body:       io.NopCloser(strings.NewReader("abcdef")),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if string(audio.Frame.Data) != "abcdef" {
		t.Fatalf("audio data = %q, want raw chunk", string(audio.Frame.Data))
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second chunk err = %v, want EOF", err)
	}
}

func TestBasetenTTSStreamSendsReferenceSetupTextAndEnd(t *testing.T) {
	setupCh := make(chan map[string]any, 1)
	textCh := make(chan string, 1)
	endCh := make(chan string, 1)
	errCh := make(chan error, 1)
	server := newBasetenTTSTestWebsocketServer(t, func(conn *websocket.Conn, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Api-Key test-key" {
			t.Errorf("Authorization = %q, want Api-Key header", got)
		}
		_, setupPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		var setup map[string]any
		if err := json.Unmarshal(setupPayload, &setup); err != nil {
			errCh <- err
			return
		}
		setupCh <- setup
		_, textPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		textCh <- string(textPayload)
		_, endPayload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		endCh <- string(endPayload)
	})
	defer server.Close()

	provider := NewBasetenTTS("test-key", "",
		WithBasetenTTSModelEndpoint(httpToWS(server.URL)),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSMaxTokens(512),
		WithBasetenTTSBufferSize(4),
	)
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push text: %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	setup := readBasetenTestChan(t, setupCh, errCh)
	assertBasetenPayload(t, setup, "voice", "emma")
	assertBasetenPayload(t, setup, "max_tokens", float64(512))
	assertBasetenPayload(t, setup, "buffer_size", float64(4))
	if got := readBasetenTestChan(t, textCh, errCh); got != "hello " {
		t.Fatalf("text message = %q, want raw text", got)
	}
	if got := readBasetenTestChan(t, endCh, errCh); got != "__END__" {
		t.Fatalf("end message = %q, want sentinel", got)
	}
}

func TestBasetenTTSStreamReturnsBinaryAudioFrames(t *testing.T) {
	server := newBasetenTTSTestWebsocketServer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03, 0x04}); err != nil {
			t.Errorf("write audio: %v", err)
			return
		}
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	})
	defer server.Close()

	provider := NewBasetenTTS("test-key", "", WithBasetenTTSModelEndpoint(httpToWS(server.URL)))
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if string(audio.Frame.Data) != "\x01\x02\x03\x04" {
		t.Fatalf("audio data = %#v, want binary websocket payload", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = %+v, want 24 kHz mono PCM", audio.Frame)
	}
	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second next err = %v, want EOF", err)
	}
}

func assertBasetenPayload(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func newBasetenTTSTestWebsocketServer(t *testing.T, handler func(*websocket.Conn, *http.Request)) *httptest.Server {
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

func readBasetenTestChan[T any](t *testing.T, ch <-chan T, errCh <-chan error) T {
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

func TestBasetenTTSStreamDialErrorReturnsFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	provider := NewBasetenTTS("test-key", "", WithBasetenTTSModelEndpoint("ws://"+addr))
	if _, err := provider.Stream(context.Background()); err == nil {
		t.Fatal("stream error = nil, want dial failure")
	}
}
