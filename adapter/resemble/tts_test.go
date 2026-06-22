package resemble

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestResembleTTSDefaultsMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	if provider.voice != "55592656" {
		t.Fatalf("voice = %q, want default voice uuid", provider.voice)
	}
	if provider.sampleRate != 44100 {
		t.Fatalf("sample rate = %d, want 44100", provider.sampleRate)
	}
	if provider.model != "" {
		t.Fatalf("model = %q, want empty by default", provider.model)
	}
	if got := coretts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := coretts.Provider(provider); got != "Resemble" {
		t.Fatalf("provider metadata = %q, want Resemble", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true for websocket streaming")
	}
}

func TestNewResembleTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "env-key")

	provider := NewResembleTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("authorization = %q, want env bearer token", got)
	}
	if got := buildResembleTTSWebsocketHeaders(provider).Get("Authorization"); got != "Bearer env-key" {
		t.Fatalf("websocket authorization = %q, want env bearer token", got)
	}

	explicit := NewResembleTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestResembleTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("RESEMBLE_API_KEY", "")
	provider := NewResembleTTS("", "")

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "RESEMBLE_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "RESEMBLE_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestResembleTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://f.cluster.resemble.ai/synthesize" {
		t.Fatalf("url = %q, want reference REST endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("accept = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "55592656")
	assertResemblePayload(t, payload, "data", "hello")
	assertResemblePayload(t, payload, "precision", "PCM_16")
	if got := payload["sample_rate"]; got != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", got)
	}
	if _, ok := payload["model"]; ok {
		t.Fatalf("model = %#v, want omitted by default", payload["model"])
	}
}

func TestResembleTTSOptionsMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "",
		WithResembleTTSVoice("voice-2"),
		WithResembleTTSSampleRate(24000),
		WithResembleTTSModel("chatterbox-turbo"),
	)

	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "voice-2")
	assertResemblePayload(t, payload, "model", "chatterbox-turbo")
	if got := payload["sample_rate"]; got != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", got)
	}
	if got := coretts.Model(provider); got != "chatterbox-turbo" {
		t.Fatalf("model metadata = %q, want chatterbox-turbo", got)
	}
}

func TestResembleTTSUpdateOptionsAffectsFutureRequests(t *testing.T) {
	provider := NewResembleTTS("test-key", "voice-1",
		WithResembleTTSSampleRate(24000),
		WithResembleTTSModel("chatterbox"),
	)

	provider.UpdateOptions(
		WithResembleTTSVoice("voice-2"),
		WithResembleTTSModel("chatterbox-turbo"),
	)

	req, err := buildResembleTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var restPayload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&restPayload); err != nil {
		t.Fatalf("decode REST body: %v", err)
	}
	assertResemblePayload(t, restPayload, "voice_uuid", "voice-2")
	assertResemblePayload(t, restPayload, "model", "chatterbox-turbo")
	if got := restPayload["sample_rate"]; got != float64(24000) {
		t.Fatalf("REST sample_rate = %#v, want unchanged 24000", got)
	}

	message, err := buildResembleTTSWebsocketMessage(provider, "hello", 9)
	if err != nil {
		t.Fatalf("build websocket message: %v", err)
	}
	var websocketPayload map[string]any
	if err := json.Unmarshal(message, &websocketPayload); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	assertResemblePayload(t, websocketPayload, "voice_uuid", "voice-2")
	assertResemblePayload(t, websocketPayload, "model", "chatterbox-turbo")
	if got := websocketPayload["sample_rate"]; got != float64(24000) {
		t.Fatalf("websocket sample_rate = %#v, want unchanged 24000", got)
	}
	if got := coretts.Model(provider); got != "chatterbox-turbo" {
		t.Fatalf("model metadata = %q, want chatterbox-turbo", got)
	}
}

func TestResembleTTSChunkedStreamDecodesReferenceResponse(t *testing.T) {
	stream := &resembleTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(`{"success":true,"audio_content":"AQI="}`)))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestResembleTTSChunkedStreamReturnsAPIError(t *testing.T) {
	stream := &resembleTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(`{"success":false,"issues":["bad voice"]}`)))},
		sampleRate: 44100,
	}

	_, err := stream.Next()
	if err == nil {
		t.Fatal("Next returned nil error, want API failure")
	}
	if got := err.Error(); got != "resemble api returned failure: bad voice" {
		t.Fatalf("error = %q, want API failure", got)
	}
}

func TestResembleTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "")

	if got := buildResembleTTSWebsocketURL(); got != "wss://websocket.cluster.resemble.ai/stream" {
		t.Fatalf("websocket URL = %q, want reference stream URL", got)
	}

	headers := buildResembleTTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestResembleTTSWebsocketMessageMatchesReference(t *testing.T) {
	provider := NewResembleTTS("test-key", "",
		WithResembleTTSVoice("voice-2"),
		WithResembleTTSSampleRate(24000),
		WithResembleTTSModel("chatterbox-turbo"),
	)

	message, err := buildResembleTTSWebsocketMessage(provider, "hello", 7)
	if err != nil {
		t.Fatalf("build websocket message: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(message, &payload); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	assertResemblePayload(t, payload, "voice_uuid", "voice-2")
	assertResemblePayload(t, payload, "data", "hello")
	assertResemblePayload(t, payload, "precision", "PCM_16")
	assertResemblePayload(t, payload, "output_format", "mp3")
	assertResemblePayload(t, payload, "model", "chatterbox-turbo")
	if payload["request_id"] != float64(7) {
		t.Fatalf("request_id = %#v, want 7", payload["request_id"])
	}
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
}

func TestResembleTTSAudioFromWebsocketMessage(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	audioPayload := `{"type":"audio","request_id":7,"audio_content":"` + base64.StdEncoding.EncodeToString(mp3Data) + `"}`
	audio, done, requestID, err := resembleTTSAudioFromWebsocketMessage([]byte(audioPayload))
	if err != nil {
		t.Fatalf("audio from websocket message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio message")
	}
	if requestID != 7 {
		t.Fatalf("request id = %d, want 7", requestID)
	}
	if audio == nil || len(audio.Frame.Data) == 0 {
		t.Fatalf("audio = %+v, want decoded audio frame", audio)
	}
	if audio.Frame.SampleRate != 48000 || audio.Frame.NumChannels != 2 {
		t.Fatalf("frame = %+v, want decoded 48000 Hz stereo mp3", audio.Frame)
	}
	prefixLen := len(audio.Frame.Data)
	if len(mp3Data) < prefixLen {
		prefixLen = len(mp3Data)
	}
	if bytes.Equal(audio.Frame.Data[:prefixLen], mp3Data[:prefixLen]) {
		t.Fatal("frame data still contains compressed mp3 bytes")
	}

	finished, done, requestID, err := resembleTTSAudioFromWebsocketMessage([]byte(`{"type":"audio_end","request_id":7}`))
	if err != nil {
		t.Fatalf("audio_end message: %v", err)
	}
	if finished == nil || !finished.IsFinal || !done || requestID != 7 {
		t.Fatalf("finished=%+v done=%v requestID=%d, want final marker for request 7", finished, done, requestID)
	}
	if finished.Frame != nil {
		t.Fatalf("final marker frame = %+v, want boundary-only marker", finished.Frame)
	}

	if _, _, _, err := resembleTTSAudioFromWebsocketMessage([]byte(`{"type":"error","message":"bad text"}`)); err == nil {
		t.Fatal("error message returned nil error, want stream error")
	}
}

func TestResembleTTSProviderCloseClosesActiveStreams(t *testing.T) {
	conn, closed := newResembleClosingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	provider := NewResembleTTS("test-key", "")
	stream := &resembleTTSSynthesizeStream{
		conn:     conn,
		ctx:      ctx,
		cancel:   cancel,
		provider: provider,
		events:   make(chan *coretts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
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
	if err := stream.PushText("again"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushText after provider Close error = %v, want closed stream error", err)
	}
}

func newResembleClosingWebsocketConn(t *testing.T) (*websocket.Conn, <-chan struct{}) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	closed := make(chan struct{})
	listener := newResembleSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		close(closed)
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
	conn, _, err := dialer.Dial("ws://resemble.test/stream", nil)
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
	return conn, closed
}

type resembleSingleConnListener struct {
	conn   net.Conn
	once   sync.Once
	closed chan struct{}
}

func newResembleSingleConnListener(conn net.Conn) *resembleSingleConnListener {
	return &resembleSingleConnListener{conn: conn, closed: make(chan struct{})}
}

func (l *resembleSingleConnListener) Accept() (net.Conn, error) {
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

func (l *resembleSingleConnListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *resembleSingleConnListener) Addr() net.Addr {
	return resembleTestAddr("resemble.test:443")
}

type resembleTestAddr string

func (a resembleTestAddr) Network() string { return "tcp" }

func (a resembleTestAddr) String() string { return string(a) }

func assertResemblePayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
