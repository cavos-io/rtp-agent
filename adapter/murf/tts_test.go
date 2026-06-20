package murf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestMurfTTSDefaultsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	if provider.baseURL != "https://global.api.murf.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "FALCON" {
		t.Fatalf("model = %q, want FALCON", provider.model)
	}
	if provider.voice != "en-US-matthew" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.style != "Conversation" {
		t.Fatalf("style = %q, want reference default style", provider.style)
	}
	if provider.encoding != "pcm" {
		t.Fatalf("encoding = %q, want pcm", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "FALCON" {
		t.Fatalf("model metadata = %q, want FALCON", got)
	}
	if got := tts.Provider(provider); got != "Murf" {
		t.Fatalf("provider metadata = %q, want Murf", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewMurfTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("MURF_API_KEY", "env-key")

	provider := NewMurfTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("api-key"); got != "env-key" {
		t.Fatalf("api-key = %q, want env key", got)
	}
	if got := buildMurfTTSWebsocketHeaders(provider).Get("api-key"); got != "env-key" {
		t.Fatalf("websocket api-key = %q, want env key", got)
	}

	explicit := NewMurfTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestMurfTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("MURF_API_KEY", "")
	provider := NewMurfTTS("", "", WithMurfTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "MURF_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "MURF_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestMurfTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://global.api.murf.ai/v1/speech/stream" {
		t.Fatalf("url = %q, want speech stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("api-key"); got != "test-key" {
		t.Fatalf("api-key = %q, want test key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "text", "hello")
	assertMurfPayload(t, payload, "model", "FALCON")
	assertMurfPayload(t, payload, "voice_id", "en-US-matthew")
	assertMurfPayload(t, payload, "style", "Conversation")
	assertMurfPayload(t, payload, "format", "pcm")
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
	if payload["multiNativeLocale"] != nil {
		t.Fatalf("multiNativeLocale = %#v, want nil by default", payload["multiNativeLocale"])
	}
}

func TestMurfTTSOptionsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSBaseURL("https://murf.example/"),
		WithMurfTTSModel("GEN2"),
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSLocale("en-US"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
		WithMurfTTSSampleRate(44100),
		WithMurfTTSEncoding("mp3"),
	)

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://murf.example/v1/speech/stream" {
		t.Fatalf("url = %q, want custom speech stream endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "model", "GEN2")
	assertMurfPayload(t, payload, "voice_id", "en-US-natalie")
	assertMurfPayload(t, payload, "multiNativeLocale", "en-US")
	assertMurfPayload(t, payload, "style", "Promo")
	if payload["rate"] != float64(12) {
		t.Fatalf("rate = %#v, want 12", payload["rate"])
	}
	if payload["pitch"] != float64(-4) {
		t.Fatalf("pitch = %#v, want -4", payload["pitch"])
	}
	assertMurfPayload(t, payload, "format", "mp3")
	if payload["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", payload["sample_rate"])
	}
}

func TestMurfTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	provider.UpdateOptions(
		WithMurfTTSLocale("en-US"),
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
	)

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "voice_id", "en-US-natalie")
	assertMurfPayload(t, payload, "multiNativeLocale", "en-US")
	assertMurfPayload(t, payload, "style", "Promo")
	if payload["rate"] != float64(12) {
		t.Fatalf("rate = %#v, want 12", payload["rate"])
	}
	if payload["pitch"] != float64(-4) {
		t.Fatalf("pitch = %#v, want -4", payload["pitch"])
	}
}

func TestMurfTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 44100,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func TestMurfTTSChunkedStreamKeepsFinalReadBytes(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(&finalReadMurfReader{data: []byte{0x01, 0x02, 0x03, 0x04}})},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("audio bytes = %#v, want final read bytes", audio.Frame.Data)
	}
	if audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("samples per channel = %d, want 2", audio.Frame.SamplesPerChannel)
	}
}

func TestMurfTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSBaseURL("https://murf.example"),
		WithMurfTTSModel("GEN2"),
		WithMurfTTSSampleRate(44100),
	)

	wsURL := buildMurfTTSWebsocketURL(provider)
	if wsURL.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", wsURL.Scheme)
	}
	if wsURL.Host != "murf.example" || wsURL.Path != "/v1/speech/stream-input" {
		t.Fatalf("websocket URL = %q, want stream-input endpoint", wsURL.String())
	}
	query := wsURL.Query()
	if query.Get("sample_rate") != "44100" {
		t.Fatalf("sample_rate query = %q, want 44100", query.Get("sample_rate"))
	}
	if query.Get("format") != "pcm" {
		t.Fatalf("format query = %q, want pcm", query.Get("format"))
	}
	if query.Get("model") != "GEN2" {
		t.Fatalf("model query = %q, want GEN2", query.Get("model"))
	}

	headers := buildMurfTTSWebsocketHeaders(provider)
	if headers.Get("api-key") != "test-key" {
		t.Fatalf("api-key = %q, want test-key", headers.Get("api-key"))
	}
}

func TestMurfTTSStreamTextAndEndPacketsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
		WithMurfTTSLocale("en-US"),
	)

	payload, err := buildMurfTTSTextMessage(provider, "hello", "context-1")
	if err != nil {
		t.Fatalf("build text message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode text message: %v", err)
	}
	if message["context_id"] != "context-1" || message["text"] != "hello " {
		t.Fatalf("message = %+v, want context and trailing-space text", message)
	}
	voiceConfig := message["voice_config"].(map[string]any)
	assertMurfPayload(t, voiceConfig, "voice_id", "en-US-natalie")
	assertMurfPayload(t, voiceConfig, "style", "Promo")
	assertMurfPayload(t, voiceConfig, "multi_native_locale", "en-US")
	if voiceConfig["rate"] != float64(12) || voiceConfig["pitch"] != float64(-4) {
		t.Fatalf("voice config = %+v, want rate and pitch", voiceConfig)
	}
	if message["min_buffer_size"] != float64(3) || message["max_buffer_delay_in_ms"] != float64(0) {
		t.Fatalf("buffer config = %+v, want reference defaults", message)
	}

	endPayload, err := buildMurfTTSEndMessage(provider, "context-1")
	if err != nil {
		t.Fatalf("build end message: %v", err)
	}
	var end map[string]any
	if err := json.Unmarshal(endPayload, &end); err != nil {
		t.Fatalf("decode end message: %v", err)
	}
	if end["context_id"] != "context-1" || end["end"] != true {
		t.Fatalf("end message = %+v, want context end packet", end)
	}
}

func TestMurfTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	conn, closed := newMurfClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   NewMurfTTS("test-key", ""),
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	defer stream.Close()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	var writeErr error
	for range 3 {
		writeErr = stream.PushText("hello")
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("PushText error = nil after closed websocket, want write failure")
	}
	if !stream.closed {
		t.Fatal("stream closed = false after write failure, want true")
	}
	err := stream.PushText("again")
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("second PushText error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close after write failure error = %v", err)
	}
}

func TestMurfTTSProviderCloseClosesActiveStreams(t *testing.T) {
	conn, closed := newMurfClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	provider := NewMurfTTS("test-key", "")
	stream := &murfTTSSynthesizeStream{
		conn:       conn,
		ctx:        ctx,
		cancel:     cancel,
		provider:   provider,
		contextID:  "context-1",
		sampleRate: 24000,
		events:     make(chan *tts.SynthesizedAudio, 1),
		errCh:      make(chan error, 1),
	}
	provider.registerStream(stream)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket close")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func newMurfClosingWebsocketConn(t *testing.T) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ready := make(chan struct{})
	closed := make(chan struct{})
	release := make(chan struct{})
	var readyOnce sync.Once
	var closedOnce sync.Once
	var releaseOnce sync.Once
	signalReady := func() {
		readyOnce.Do(func() {
			close(ready)
		})
	}
	signalClosed := func() {
		closedOnce.Do(func() {
			close(closed)
		})
	}
	releaseServer := func() {
		releaseOnce.Do(func() {
			close(release)
		})
	}
	listener := newMurfSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			signalReady()
			signalClosed()
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		signalReady()
		<-release
		_ = conn.Close()
		signalClosed()
	})}
	serverErr := make(chan error, 1)
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
	conn, _, err := dialer.Dial("ws://murf.test/v1/speech/stream-input", nil)
	if err != nil {
		releaseServer()
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		releaseServer()
		t.Fatal("timed out waiting for test websocket upgrade")
	}
	releaseServer()
	t.Cleanup(func() {
		releaseServer()
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
	return conn, closed
}

type murfSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newMurfSingleConnListener(conn net.Conn) *murfSingleConnListener {
	return &murfSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *murfSingleConnListener) Accept() (net.Conn, error) {
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

func (l *murfSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *murfSingleConnListener) Addr() net.Addr {
	return murfTestAddr("murf.test:443")
}

type murfTestAddr string

func (a murfTestAddr) Network() string { return "tcp" }

func (a murfTestAddr) String() string { return string(a) }

func TestMurfTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := murfAudioFromStreamMessage([]byte(`{"context_id":"context-1","audio":"AQIDBA=="}`), 24000)
	if err != nil {
		t.Fatalf("audio from stream message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	finished, done, err := murfAudioFromStreamMessage([]byte(`{"context_id":"context-1","final":true}`), 24000)
	if err != nil {
		t.Fatalf("final message: %v", err)
	}
	if finished != nil || !done {
		t.Fatalf("finished=%+v done=%v, want done with no audio", finished, done)
	}
}

func TestMurfTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewMurfTTS("test-key", "")
}

func assertMurfPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type finalReadMurfReader struct {
	data []byte
	done bool
}

func (r *finalReadMurfReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	copy(p, r.data)
	r.done = true
	return len(r.data), io.EOF
}
