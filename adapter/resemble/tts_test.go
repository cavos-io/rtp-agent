package resemble

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
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

	"github.com/cavos-io/rtp-agent/core/llm"
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

func TestResembleTTSSynthesizeReturnsAPIStatusError(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: resembleRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader(`{"error":"rate limited"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewResembleTTS("test-key", "")

	stream, err := provider.Synthesize(context.Background(), "hello")
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

func TestResembleTTSChunkedStreamDecodesReferenceWAVResponse(t *testing.T) {
	pcm := []byte{0x01, 0x00, 0x02, 0x00}
	wav := resembleTestWAV(pcm, 24000, 1)
	payload := `{"success":true,"audio_content":"` + base64.StdEncoding.EncodeToString(wav) + `"}`
	stream := &resembleTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(payload)))},
		sampleRate: 24000,
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if !bytes.Equal(audio.Frame.Data, pcm) {
		t.Fatalf("audio data = %#v, want decoded PCM %#v", audio.Frame.Data, pcm)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 || audio.Frame.SamplesPerChannel != 2 {
		t.Fatalf("frame = rate %d channels %d samples %d, want 24000/1/2", audio.Frame.SampleRate, audio.Frame.NumChannels, audio.Frame.SamplesPerChannel)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second audio = %#v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("third Next error = %v, want EOF", err)
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

func TestResembleTTSStreamSendsSentencesAndFlushesTailLikeReference(t *testing.T) {
	conn, messages := newResembleRecordingWebsocketConn(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &resembleTTSSynthesizeStream{
		conn:     conn,
		ctx:      ctx,
		cancel:   cancel,
		provider: NewResembleTTS("test-key", ""),
		events:   make(chan *coretts.SynthesizedAudio, 1),
		errCh:    make(chan error, 1),
	}
	defer stream.Close()

	if err := stream.PushText("This first sentence is definitely long enough. Tail"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	first := readResembleTTSStreamMessage(t, messages)
	if first["data"] != "This first sentence is definitely long enough." || first["request_id"] != float64(1) {
		t.Fatalf("first websocket message = %#v, want completed sentence request 1", first)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	tail := readResembleTTSStreamMessage(t, messages)
	if tail["data"] != "Tail" || tail["request_id"] != float64(2) {
		t.Fatalf("tail websocket message = %#v, want flushed tail request 2", tail)
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
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestResembleTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
	var httpCalls int
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: resembleRoundTripFunc(func(*http.Request) (*http.Response, error) {
		httpCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"audio_content":""}`)),
		}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()

	provider := NewResembleTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if httpCalls != 0 {
		t.Fatalf("HTTP calls after Close = %d, want 0", httpCalls)
	}
}

func TestResembleTTSStreamAfterCloseIsRejected(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	dialCalls := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dialCalls++
			return nil, errors.New("unexpected websocket dial")
		},
		Proxy: nil,
	}
	defer func() { websocket.DefaultDialer = oldDialer }()

	provider := NewResembleTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream = %#v, want nil after Close", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("websocket dials after Close = %d, want 0", dialCalls)
	}
}

func TestResembleTTSStreamUnexpectedCloseReturnsAPIStatusError(t *testing.T) {
	conn := newResembleProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &resembleTTSSynthesizeStream{
		conn:   conn,
		events: make(chan *coretts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseUnsupportedData {
			t.Fatalf("StatusCode = %d, want close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Resemble connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Resemble close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func TestResembleTTSStreamNormalCloseBeforeAudioEndReturnsAPIStatusError(t *testing.T) {
	conn := newResembleProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &resembleTTSSynthesizeStream{
		conn:   conn,
		events: make(chan *coretts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var statusErr *llm.APIStatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("readLoop error = %T %v, want APIStatusError", err, err)
		}
		if statusErr.StatusCode != websocket.CloseNormalClosure {
			t.Fatalf("StatusCode = %d, want normal close code", statusErr.StatusCode)
		}
		if !strings.Contains(err.Error(), "Resemble connection closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Resemble close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket normal close error")
	}
}

type resembleRoundTripFunc func(*http.Request) (*http.Response, error)

func (f resembleRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func resembleTestWAV(pcm []byte, sampleRate uint32, channels uint16) []byte {
	var wav bytes.Buffer
	byteRate := sampleRate * uint32(channels) * 2
	blockAlign := channels * 2
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+len(pcm)))
	wav.WriteString("WAVE")
	wav.WriteString("fmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, channels)
	_ = binary.Write(&wav, binary.LittleEndian, sampleRate)
	_ = binary.Write(&wav, binary.LittleEndian, byteRate)
	_ = binary.Write(&wav, binary.LittleEndian, blockAlign)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)))
	wav.Write(pcm)
	return wav.Bytes()
}

func newResembleProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newResembleSingleConnListener(serverConn)
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
	return conn
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

func newResembleRecordingWebsocketConn(t *testing.T) (*websocket.Conn, <-chan map[string]any) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	messages := make(chan map[string]any, 4)
	ready := make(chan struct{})
	var readyOnce sync.Once
	listener := newResembleSingleConnListener(serverConn)
	upgrader := websocket.Upgrader{}
	serverErr := make(chan error, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			readyOnce.Do(func() { close(ready) })
			serverErr <- err
			return
		}
		readyOnce.Do(func() { close(ready) })
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(payload, &msg); err != nil {
				serverErr <- err
				return
			}
			messages <- msg
		}
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
	conn, _, err := dialer.Dial("ws://resemble.test/stream", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial test websocket: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for test websocket upgrade")
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
	return conn, messages
}

func readResembleTTSStreamMessage(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Resemble TTS websocket message")
	}
	return nil
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
