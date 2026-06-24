package gnani

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

type gnaniTTSCloseErrorBody struct {
	closed bool
}

func (b *gnaniTTSCloseErrorBody) Read(_ []byte) (int, error) {
	if b.closed {
		return 0, errors.New("read after close")
	}
	return 0, io.EOF
}

func (b *gnaniTTSCloseErrorBody) Close() error {
	b.closed = true
	return nil
}

func TestGnaniTTSDefaultsMatchReference(t *testing.T) {
	provider := NewTTS("test-key")

	if provider.baseURL != "https://api.vachana.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.voice != "Karan" {
		t.Fatalf("voice = %q, want Karan", provider.voice)
	}
	if provider.model != "vachana-voice-v3" {
		t.Fatalf("model = %q, want vachana-voice-v3", provider.model)
	}
	if got := tts.Model(provider); got != "vachana-voice-v3" {
		t.Fatalf("model metadata = %q, want vachana-voice-v3", got)
	}
	if got := tts.Provider(provider); got != "Gnani" {
		t.Fatalf("provider metadata = %q, want Gnani", got)
	}
	if provider.encoding != "linear_pcm" {
		t.Fatalf("encoding = %q, want linear_pcm", provider.encoding)
	}
	if provider.container != "wav" {
		t.Fatalf("container = %q, want wav", provider.container)
	}
	if provider.sampleWidth != 2 {
		t.Fatalf("sample width = %d, want 2", provider.sampleWidth)
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("num channels = %d, want 1", provider.NumChannels())
	}
	if provider.language != "hi" {
		t.Fatalf("language = %q, want hi", provider.language)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming capability = false, want true for websocket streaming")
	}
}

func TestNewGnaniTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "env-key")

	provider := NewTTS("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTTS("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGnaniTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GNANI_API_KEY", "")
	provider := NewTTS("", WithBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "namaste")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GNANI_API_KEY") {
		t.Fatalf("Synthesize error = %q, want GNANI_API_KEY guidance", err)
	}

	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error before flush: %v", err)
	}
	if err := stream.PushText("namaste"); err != nil {
		t.Fatalf("PushText returned error: %v", err)
	}
	err = stream.Flush()
	if err == nil {
		t.Fatal("Flush returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GNANI_API_KEY") {
		t.Fatalf("Flush error = %q, want GNANI_API_KEY guidance", err)
	}
}

func TestGnaniTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewTTS("test-key")

	req, err := buildTTSRequest(context.Background(), provider, "namaste")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.vachana.ai/api/v1/tts/inference" {
		t.Fatalf("url = %q, want inference endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGnaniPayload(t, payload, "text", "namaste")
	assertGnaniPayload(t, payload, "voice", "Karan")
	assertGnaniPayload(t, payload, "model", "vachana-voice-v3")

	audioConfig := payload["audio_config"].(map[string]any)
	assertGnaniPayload(t, audioConfig, "encoding", "linear_pcm")
	assertGnaniPayload(t, audioConfig, "container", "wav")
	if audioConfig["sample_rate"] != float64(16000) {
		t.Fatalf("sample_rate = %#v, want 16000", audioConfig["sample_rate"])
	}
	if audioConfig["num_channels"] != float64(1) {
		t.Fatalf("num_channels = %#v, want 1", audioConfig["num_channels"])
	}
	if audioConfig["sample_width"] != float64(2) {
		t.Fatalf("sample_width = %#v, want 2", audioConfig["sample_width"])
	}
}

func TestGnaniTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: gnaniTTSRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewTTS("test-key")

	stream, err := provider.Synthesize(context.Background(), "namaste")
	if err == nil {
		defer stream.Close()
		t.Fatal("Synthesize returned nil error, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Synthesize error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want 429", statusErr.StatusCode)
	}
	if body, ok := statusErr.Body.(string); !ok || body != `{"error":"rate limited"}` {
		t.Fatalf("body = %#v, want provider response body", statusErr.Body)
	}
}

func TestGnaniTTSOptionsMatchReference(t *testing.T) {
	provider := NewTTS("test-key",
		WithBaseURL("https://gnani.example/"),
		WithVoice("Riya"),
		WithModel("custom-model"),
		WithSampleRate(44100),
		WithEncoding("oggopus"),
		WithContainer("ogg"),
		WithNumChannels(2),
		WithSampleWidth(4),
		WithLanguage("ta"),
	)

	req, err := buildTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://gnani.example/api/v1/tts/inference" {
		t.Fatalf("url = %q, want custom endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertGnaniPayload(t, payload, "voice", "Riya")
	assertGnaniPayload(t, payload, "model", "custom-model")
	audioConfig := payload["audio_config"].(map[string]any)
	assertGnaniPayload(t, audioConfig, "encoding", "oggopus")
	assertGnaniPayload(t, audioConfig, "container", "ogg")
	if audioConfig["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", audioConfig["sample_rate"])
	}
	if audioConfig["num_channels"] != float64(2) {
		t.Fatalf("num_channels = %#v, want 2", audioConfig["num_channels"])
	}
	if audioConfig["sample_width"] != float64(4) {
		t.Fatalf("sample_width = %#v, want 4", audioConfig["sample_width"])
	}
	if provider.language != "ta" {
		t.Fatalf("language = %q, want ta", provider.language)
	}
}

func TestGnaniTTSChunkedStreamStripsWAVHeaderAndUsesConfiguredSampleRate(t *testing.T) {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	wav := append(header, []byte{0x01, 0x02, 0x03, 0x04}...)
	stream := &ttsChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(bytes.NewReader(wav))},
		sampleRate:  44100,
		numChannels: 2,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio data = %#v, want WAV payload", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("num channels = %d, want configured channels", audio.Frame.NumChannels)
	}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestGnaniTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	wav := gnaniTestWAV([]byte{0x01, 0x02})
	stream := &ttsChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(bytes.NewReader(wav))},
		sampleRate:  16000,
		numChannels: 1,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next audio error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next final error = %v", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("final Next = %+v, want final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
	}
}

func TestGnaniTTSChunkedStreamEmitsReferenceFinalMarkerAfterEmptyAudio(t *testing.T) {
	stream := &ttsChunkedStream{
		resp:        &http.Response{Body: io.NopCloser(bytes.NewReader(nil))},
		sampleRate:  16000,
		numChannels: 1,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want final marker", err)
	}
	if audio == nil || !audio.IsFinal || audio.Frame != nil {
		t.Fatalf("Next = %+v, want final marker", audio)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next error = %v, want EOF", err)
	}
}

func TestGnaniTTSChunkedStreamNextAfterCloseReturnsEOF(t *testing.T) {
	body := &gnaniTTSCloseErrorBody{}
	stream := &ttsChunkedStream{
		resp:        &http.Response{Body: body},
		sampleRate:  16000,
		numChannels: 1,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("Next after Close = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestGnaniTTSWebsocketURLHeadersAndPayloadMatchReference(t *testing.T) {
	provider := NewTTS("test-key", WithBaseURL("https://gnani.example/"), WithLanguage("ta"))

	wsURL := buildGnaniTTSWebsocketURL(provider)
	if wsURL.String() != "wss://gnani.example/api/v1/tts" {
		t.Fatalf("websocket URL = %q, want reference endpoint", wsURL.String())
	}

	httpProvider := NewTTS("test-key", WithBaseURL("http://gnani.example"))
	httpURL := buildGnaniTTSWebsocketURL(httpProvider)
	if httpURL.String() != "ws://gnani.example/api/v1/tts" {
		t.Fatalf("http websocket URL = %q, want ws endpoint", httpURL.String())
	}

	headers := buildGnaniTTSWebsocketHeaders(provider)
	if got := headers.Get("X-API-Key-ID"); got != "test-key" {
		t.Fatalf("X-API-Key-ID = %q, want test-key", got)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	message, err := buildGnaniTTSWebsocketRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build websocket request: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(message, &payload); err != nil {
		t.Fatalf("decode websocket payload: %v", err)
	}
	assertGnaniPayload(t, payload, "text", "hello")
	assertGnaniPayload(t, payload, "language", "ta")
	assertGnaniPayload(t, payload, "voice", "Karan")
}

func TestGnaniTTSAudioFromWebsocketMessageStripsWAVAndDetectsComplete(t *testing.T) {
	wav := gnaniTestWAV([]byte{0x01, 0x02, 0x03, 0x04})
	message := []byte(`{"type":"audio","data":{"audio":"` + base64.StdEncoding.EncodeToString(wav) + `"}}`)

	audio, done, err := gnaniTTSAudioFromWebsocketMessage(message, 16000, 1)
	if err != nil {
		t.Fatalf("parse audio message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio data = %#v, want stripped WAV payload", audio.Frame.Data)
	}

	completeMessage := []byte(`{"type":"complete","data":{"audio":"` + base64.StdEncoding.EncodeToString(wav) + `"}}`)
	audio, done, err = gnaniTTSAudioFromWebsocketMessage(completeMessage, 16000, 1)
	if err != nil {
		t.Fatalf("parse complete message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for complete message")
	}
	if !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("complete audio data = %#v, want stripped WAV payload", audio.Frame.Data)
	}

	final, done, err := gnaniTTSAudioFromWebsocketMessage([]byte(`{"type":"complete","data":{"audio":""}}`), 16000, 1)
	if err != nil {
		t.Fatalf("parse empty complete message: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for empty complete message")
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("final=%+v, want reference final marker", final)
	}
	if final.Frame != nil {
		t.Fatalf("final frame = %+v, want boundary-only final marker", final.Frame)
	}

	if _, _, err := gnaniTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`), 16000, 1); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	} else {
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("error message error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status code = %d, want 500", statusErr.StatusCode)
		}
		if statusErr.Body != "bad text" {
			t.Fatalf("body = %#v, want bad text", statusErr.Body)
		}
	}
}

func TestGnaniTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &gnaniTTSSynthesizeStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error),
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestGnaniTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &gnaniTTSSynthesizeStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestGnaniTTSStreamUnexpectedCloseReturnsAPIConnectionError(t *testing.T) {
	conn := newGnaniProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &gnaniTTSSynthesizeStream{
		conn:        conn,
		ctx:         ctx,
		cancel:      cancel,
		sampleRate:  16000,
		numChannels: 1,
		events:      make(chan *tts.SynthesizedAudio, 1),
		errCh:       make(chan error, 1),
	}
	go stream.readLoop()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Gnani TTS WebSocket closed") {
		t.Fatalf("Next error = %q, want Gnani close context", err)
	}
}

func TestGnaniTTSStreamNormalCloseBeforeCompleteReturnsAPIConnectionError(t *testing.T) {
	conn := newGnaniProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &gnaniTTSSynthesizeStream{
		conn:        conn,
		ctx:         ctx,
		cancel:      cancel,
		sampleRate:  16000,
		numChannels: 1,
		events:      make(chan *tts.SynthesizedAudio, 1),
		errCh:       make(chan error, 1),
	}
	go stream.readLoop()

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next error = nil, want APIConnectionError")
	}
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "Gnani TTS WebSocket closed") {
		t.Fatalf("Next error = %q, want Gnani close context", err)
	}
}

func newGnaniProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newGnaniSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, ""),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			serverErr <- err
		}
	}()
	dialer := websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	conn, _, err := dialer.Dial("ws://gnani.test/api/v1/tts", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		_ = conn.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case err := <-serverErr:
			t.Errorf("test websocket server error: %v", err)
		default:
		}
	})
	return conn
}

type gnaniSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newGnaniSingleConnListener(conn net.Conn) *gnaniSingleConnListener {
	return &gnaniSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *gnaniSingleConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() {
		conn = l.conn
	})
	if conn != nil {
		return conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *gnaniSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *gnaniSingleConnListener) Addr() net.Addr {
	return gnaniTestAddr("gnani.test:443")
}

type gnaniTestAddr string

func (a gnaniTestAddr) Network() string { return "tcp" }

func (a gnaniTestAddr) String() string { return string(a) }

func TestGnaniTTSStreamEmptyFlushCompletesWithoutDialing(t *testing.T) {
	provider := NewTTS("test-key")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next error = %v, want EOF", err)
	}
}

func TestGnaniTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewTTS("test-key")
}

func assertGnaniPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func gnaniTestWAV(payload []byte) []byte {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	return append(header, payload...)
}

type gnaniTTSRoundTripFunc func(*http.Request) (*http.Response, error)

func (f gnaniTTSRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
