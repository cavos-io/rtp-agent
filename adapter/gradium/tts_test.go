package gradium

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
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	coretts "github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestGradiumTTSDefaultsMatchReference(t *testing.T) {
	provider := NewGradiumTTS("test-key", "")

	if provider.modelEndpoint != "wss://api.gradium.ai/api/speech/tts" {
		t.Fatalf("model endpoint = %q, want reference websocket endpoint", provider.modelEndpoint)
	}
	if provider.modelName != "default" {
		t.Fatalf("model name = %q, want default", provider.modelName)
	}
	if provider.voice != "" {
		t.Fatalf("voice = %q, want unset voice", provider.voice)
	}
	if provider.voiceID != "YTpq7expH9539ERJ" {
		t.Fatalf("voice id = %q, want reference default voice id", provider.voiceID)
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want 48000", provider.SampleRate())
	}
	if got := coretts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := coretts.Provider(provider); got != "Gradium" {
		t.Fatalf("provider metadata = %q, want Gradium", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true")
	}
}

func TestNewGradiumTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "env-key")

	provider := NewGradiumTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewGradiumTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGradiumTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "")
	provider := NewGradiumTTS("", "", WithGradiumTTSModelEndpoint("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GRADIUM_API_KEY") {
		t.Fatalf("Synthesize error = %q, want GRADIUM_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GRADIUM_API_KEY") {
		t.Fatalf("Stream error = %q, want GRADIUM_API_KEY guidance", err)
	}
}

func TestGradiumTTSSynthesizeDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("gradium tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewGradiumTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	_, err = stream.Next()
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func TestGradiumTTSSynthesizeDefersReferenceConnectUntilNext(t *testing.T) {
	dials := 0
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("gradium tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewGradiumTTS("test-key", "")
	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	if dials != 0 {
		t.Fatalf("dials before Next = %d, want 0", dials)
	}

	_, err = stream.Next()
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
	if dials != 1 {
		t.Fatalf("dials after Next = %d, want 1", dials)
	}

	closedStream, err := provider.Synthesize(context.Background(), "cancelled")
	if err != nil {
		t.Fatalf("second Synthesize error = %v", err)
	}
	if err := closedStream.Close(); err != nil {
		t.Fatalf("Close before Next error = %v", err)
	}
	if audio, err := closedStream.Next(); audio != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Next after close = (%#v, %v), want nil EOF", audio, err)
	}
	if dials != 1 {
		t.Fatalf("dials after close-before-Next = %d, want 1", dials)
	}
}

func TestGradiumTTSStreamDefersReferenceConnectUntilText(t *testing.T) {
	dials := 0
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("gradium tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewGradiumTTS("test-key", "")
	stream, err := provider.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream error = %v, want lazy reference stream", err)
	}
	if dials != 0 {
		t.Fatalf("dials before text = %d, want 0", dials)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close before text error = %v", err)
	}
	if dials != 0 {
		t.Fatalf("dials after close-before-text = %d, want 0", dials)
	}
}

func TestGradiumTTSStreamDialFailureReturnsAPIConnectionError(t *testing.T) {
	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("gradium tts dial failed")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	provider := NewGradiumTTS("test-key", "")
	stream, err := provider.Stream(context.Background())

	if err != nil {
		t.Fatalf("Stream error = %v, want lazy reference stream", err)
	}
	if stream == nil {
		t.Fatal("Stream = nil, want lazy reference stream")
	}
	err = stream.PushText("hello world")
	if err == nil {
		t.Fatal("PushText error = nil, want websocket dial failure")
	}
	var apiErr *llm.APIConnectionError
	if !errors.As(err, &apiErr) {
		t.Fatalf("PushText error = %T %v, want APIConnectionError", err, err)
	}
}

func TestGradiumTTSOptionsBuildReferenceSetupAndHeaders(t *testing.T) {
	provider := NewGradiumTTS("test-key", "Ava",
		WithGradiumTTSModelEndpoint("wss://gradium.example/tts"),
		WithGradiumTTSModelName("custom"),
		WithGradiumTTSVoiceID("voice-1"),
		WithGradiumTTSPronunciationID("pron-1"),
		WithGradiumTTSJSONConfig(map[string]any{"style": "calm"}),
	)

	if provider.modelEndpoint != "wss://gradium.example/tts" {
		t.Fatalf("model endpoint = %q, want custom endpoint", provider.modelEndpoint)
	}
	setup, err := buildGradiumTTSSetup(provider)
	if err != nil {
		t.Fatalf("build setup: %v", err)
	}
	assertGradiumSetup(t, setup, "type", "setup")
	assertGradiumSetup(t, setup, "model_name", "custom")
	assertGradiumSetup(t, setup, "output_format", "pcm")
	assertGradiumSetup(t, setup, "voice", "Ava")
	assertGradiumSetup(t, setup, "voice_id", "voice-1")
	assertGradiumSetup(t, setup, "pronunciation_id", "pron-1")
	config := setup["json_config"].(string)
	if config != `{"style":"calm"}` {
		t.Fatalf("json_config = %q, want encoded config", config)
	}

	headers := buildGradiumTTSHeaders(provider)
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", headers.Get("x-api-key"))
	}
	if headers.Get("x-api-source") != "livekit" {
		t.Fatalf("x-api-source = %q, want livekit", headers.Get("x-api-source"))
	}
}

func TestGradiumTTSSetupOmitsUnsetOptionalFields(t *testing.T) {
	provider := NewGradiumTTS("test-key", "",
		WithGradiumTTSModelEndpoint("wss://gradium.example/tts/"),
		WithGradiumTTSVoiceID(""),
		WithGradiumTTSPronunciationID(""),
	)

	if provider.modelEndpoint != "wss://gradium.example/tts" {
		t.Fatalf("model endpoint = %q, want trimmed endpoint", provider.modelEndpoint)
	}
	setup, err := buildGradiumTTSSetup(provider)
	if err != nil {
		t.Fatalf("build setup: %v", err)
	}
	if _, ok := setup["voice"]; ok {
		t.Fatalf("voice present in setup: %#v", setup)
	}
	if _, ok := setup["voice_id"]; ok {
		t.Fatalf("voice_id present in setup: %#v", setup)
	}
	if _, ok := setup["pronunciation_id"]; ok {
		t.Fatalf("pronunciation_id present in setup: %#v", setup)
	}
}

func TestGradiumTTSSetupRejectsInvalidJSONConfig(t *testing.T) {
	provider := NewGradiumTTS("test-key", "Ava",
		WithGradiumTTSJSONConfig(map[string]any{"bad": func() {}}),
	)

	if _, err := buildGradiumTTSSetup(provider); err == nil {
		t.Fatal("build setup error = nil, want invalid json config error")
	}
	if setup := mustBuildGradiumTTSSetup(provider); len(setup) != 0 {
		t.Fatalf("must setup = %#v, want empty setup on json config error", setup)
	}
}

func TestGradiumTTSTextAndEndMessagesMatchReference(t *testing.T) {
	textMessage := buildGradiumTTSTextMessage("hello")
	assertGradiumSetup(t, textMessage, "type", "text")
	assertGradiumSetup(t, textMessage, "text", "hello")

	endMessage := buildGradiumTTSEndMessage()
	assertGradiumSetup(t, endMessage, "type", "end_of_stream")
}

func TestGradiumTTSStreamTokenizesWordsAndFlushesTailLikeReference(t *testing.T) {
	var writes []map[string]any
	stream := &gradiumTTSSynthesizeStream{
		writeMessage: func(payload map[string]any) error {
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes after PushText = %d, want completed word only", len(writes))
	}
	assertGradiumSetup(t, writes[0], "text", "hello ")

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 2 {
		t.Fatalf("writes after Flush = %d, want tail word only", len(writes))
	}
	assertGradiumSetup(t, writes[1], "text", "world ")
}

func TestGradiumTTSStreamEndInputSendsReferenceEndOnce(t *testing.T) {
	var writes []map[string]any
	stream := &gradiumTTSSynthesizeStream{
		writeMessage: func(payload map[string]any) error {
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.PushText("hello world"); err != nil {
		t.Fatalf("PushText error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput error = %v", err)
	}
	if err := stream.EndInput(); err != nil {
		t.Fatalf("second EndInput error = %v", err)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after EndInput error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after EndInput error = %v, want io.ErrClosedPipe", err)
	}

	if len(writes) != 3 {
		t.Fatalf("writes = %d, want completed word, tail word, and end", len(writes))
	}
	assertGradiumSetup(t, writes[0], "text", "hello ")
	assertGradiumSetup(t, writes[1], "text", "world ")
	assertGradiumSetup(t, writes[2], "type", "end_of_stream")
}

func TestGradiumTTSClosedStreamRejectsTextAndFlush(t *testing.T) {
	var writes []map[string]any
	stream := &gradiumTTSSynthesizeStream{
		closed: true,
		writeMessage: func(payload map[string]any) error {
			writes = append(writes, payload)
			return nil
		},
	}

	if err := stream.PushText("hello world"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after Close error = %v, want io.ErrClosedPipe", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes after Close = %d, want none", len(writes))
	}
}

func TestGradiumTTSWebsocketMessageMapsAudioAndEnd(t *testing.T) {
	audio, done, err := gradiumTTSAudioFromMessage([]byte(`{"type":"audio","audio":"`+base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})+`"}`), 48000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if audio.Frame.SampleRate != 48000 || !bytes.Equal(audio.Frame.Data, []byte{0x01, 0x02}) {
		t.Fatalf("audio = %+v, want decoded 48k frame", audio.Frame)
	}

	audio, done, err = gradiumTTSAudioFromMessage([]byte(`{"type":"end_of_stream"}`), 48000)
	if err != nil {
		t.Fatalf("end from message: %v", err)
	}
	if audio == nil || !audio.IsFinal || !done {
		t.Fatalf("audio=%v done=%v, want final marker and done", audio, done)
	}
	if audio.Frame != nil {
		t.Fatalf("final marker frame = %+v, want no audio frame", audio.Frame)
	}
}

func TestGradiumTTSWebsocketMalformedPayloadReturnsAPIConnectionError(t *testing.T) {
	cases := [][]byte{
		[]byte(`{`),
		[]byte(`{"type":"audio","audio":"abcde"}`),
	}
	for _, payload := range cases {
		audio, done, err := gradiumTTSAudioFromMessage(payload, 48000)
		if err == nil {
			t.Fatalf("payload %s returned nil error", payload)
		}
		if audio != nil || done {
			t.Fatalf("payload %s audio=%+v done=%v, want no audio and not done", payload, audio, done)
		}
		var apiErr *llm.APIConnectionError
		if !errors.As(err, &apiErr) {
			t.Fatalf("payload %s error = %T %v, want APIConnectionError", payload, err, err)
		}
	}
}

func TestGradiumTTSWebsocketChunkedStreamUsesPresetConnection(t *testing.T) {
	conn := newGradiumProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)
	oldDialer := websocket.DefaultDialer
	dials := 0
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			dials++
			return nil, errors.New("unexpected dial")
		},
		Proxy: nil,
	}
	t.Cleanup(func() { websocket.DefaultDialer = oldDialer })

	stream := &gradiumTTSWebsocketChunkedStream{
		ctx:           context.Background(),
		modelEndpoint: "://bad-url",
		conn:          conn,
		sampleRate:    48000,
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want preset websocket close marker", err)
	}
	if final == nil || !final.IsFinal {
		t.Fatalf("Next = %#v, want final marker from preset websocket", final)
	}
	if dials != 0 {
		t.Fatalf("dials = %d, want no dial when conn is preset", dials)
	}
	if !stream.started {
		t.Fatal("stream.started = false, want preset connection path marked started")
	}
	if stream.closed {
		t.Fatal("stream.closed = true, want provider close to produce final marker without marking stream closed")
	}
	if !stream.completed {
		t.Fatal("stream.completed = false, want provider close to complete chunk stream")
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after provider close error = %v, want EOF", err)
	}
}

func TestGradiumTTSWebsocketMessageIgnoresReferenceEmptyBase64Noise(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`{"type":"audio","audio":"!!!!"}`),
		[]byte(`{"type":"audio","audio":"==="}`),
	} {
		audio, done, err := gradiumTTSAudioFromMessage(payload, 48000)
		if err != nil {
			t.Fatalf("audio from noise %s: %v", payload, err)
		}
		if audio != nil || done {
			t.Fatalf("noise %s = audio:%+v done:%v, want ignored empty chunk", payload, audio, done)
		}
	}
}

func TestGradiumTTSWebsocketCloseEmitsReferenceFinalMarker(t *testing.T) {
	conn := newGradiumProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &gradiumTTSWebsocketChunkedStream{conn: conn, sampleRate: 48000}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want reference final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestGradiumTTSWebsocketNonNormalCloseEmitsReferenceFinalMarker(t *testing.T) {
	conn := newGradiumProviderCloseWebsocketConn(t, websocket.CloseUnsupportedData)

	stream := &gradiumTTSWebsocketChunkedStream{conn: conn, sampleRate: 48000}
	final, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want reference final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("Next = %+v, want boundary-only final marker", final)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next after final marker error = %v, want EOF", err)
	}
}

func TestGradiumTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &gradiumTTSSynthesizeStream{
		ctx:        ctx,
		cancel:     cancel,
		sampleRate: 48000,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestGradiumTTSClosedStreamNextIgnoresProviderClose(t *testing.T) {
	conn := newGradiumProviderCloseWebsocketConn(t, websocket.CloseNormalClosure)

	stream := &gradiumTTSSynthesizeStream{
		ctx:        context.Background(),
		conn:       conn,
		sampleRate: 48000,
		closed:     true,
	}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func newGradiumProviderCloseWebsocketConn(t *testing.T, closeCode int) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	listener := newGradiumSingleConnListener(serverConn)
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

	conn, _, err := dialer.Dial("ws://gradium.test/api/speech/tts", nil)
	if err != nil {
		clientConn.Close()
		t.Fatalf("dial websocket: %v", err)
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

func assertGradiumSetup(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		encoded, _ := json.Marshal(payload)
		t.Fatalf("%s = %#v, want %q in %s", key, got, want, encoded)
	}
}
