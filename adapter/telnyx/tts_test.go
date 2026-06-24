package telnyx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/gorilla/websocket"
)

func TestTelnyxTTSDefaultsMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "")

	if provider.voice != "Telnyx.NaturalHD.astra" {
		t.Fatalf("voice = %q, want reference default", provider.voice)
	}
	if provider.baseURL != "wss://api.telnyx.com/v2/text-to-speech/speech" {
		t.Fatalf("base URL = %q, want reference websocket endpoint", provider.baseURL)
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.SampleRate())
	}
	if got := tts.Model(provider); got != "Telnyx.NaturalHD.astra" {
		t.Fatalf("model metadata = %q, want reference voice", got)
	}
	if got := tts.Provider(provider); got != "telnyx" {
		t.Fatalf("provider metadata = %q, want telnyx", got)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewTelnyxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	provider := NewTelnyxTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTelnyxTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestTelnyxTTSStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "")
	provider := NewTelnyxTTS("", "", WithTelnyxTTSBaseURL("://bad-url"))

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "TELNYX_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestTelnyxTTSStreamURLAndHeadersMatchReference(t *testing.T) {
	provider := NewTelnyxTTS("test-key", "voice-1", WithTelnyxTTSBaseURL("wss://telnyx.example/speech"))

	streamURL, err := url.Parse(buildTelnyxTTSStreamURL(provider))
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if streamURL.Scheme != "wss" || streamURL.Host != "telnyx.example" || streamURL.Path != "/speech" {
		t.Fatalf("stream URL = %q, want configured websocket URL", streamURL.String())
	}
	if streamURL.Query().Get("voice") != "voice-1" {
		t.Fatalf("voice query = %q, want voice-1", streamURL.Query().Get("voice"))
	}

	headers := buildTelnyxTTSHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
}

func TestTelnyxTTSTextMessagesMatchReference(t *testing.T) {
	warmup := buildTelnyxTTSTextMessage(" ")
	text := buildTelnyxTTSTextMessage("hello")
	flush := buildTelnyxTTSTextMessage("")

	assertTelnyxTextPayload(t, warmup, " ")
	assertTelnyxTextPayload(t, text, "hello")
	assertTelnyxTextPayload(t, flush, "")
}

func TestTelnyxTTSStreamBuffersTextUntilFlushLikeReference(t *testing.T) {
	var writes []string
	stream := &telnyxTTSStream{
		writeMessage: func(message map[string]string) error {
			writes = append(writes, message["text"])
			return nil
		},
	}

	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("PushText first error = %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("PushText second error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes after PushText = %#v, want buffered text with no websocket writes", writes)
	}

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	want := []string{"hello world", ""}
	if !reflect.DeepEqual(writes, want) {
		t.Fatalf("writes after Flush = %#v, want %#v", writes, want)
	}
}

func TestTelnyxTTSStreamClosesAfterTextWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
		writeMessage: func(map[string]string) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushText("hello"); err != nil {
		t.Fatalf("PushText error = %v, want buffered text accepted", err)
	}
	if err := stream.Flush(); !errors.Is(err, writeErr) {
		t.Fatalf("Flush error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestTelnyxTTSProviderCloseClosesActiveStreams(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewTelnyxTTS("test-key", "")
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after provider Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after provider Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestTelnyxTTSStreamNextAfterCloseReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &telnyxTTSStream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *tts.SynthesizedAudio),
		errCh:  make(chan error),
		closeConn: func() error {
			return nil
		},
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	_, err := stream.Next()

	if err != io.EOF {
		t.Fatalf("Next after Close error = %v, want EOF", err)
	}
}

func TestTelnyxTTSClosedStreamNextIgnoresQueuedAudio(t *testing.T) {
	stream := &telnyxTTSStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error),
		closed: true,
	}
	stream.events <- &tts.SynthesizedAudio{RequestID: "stale"}

	audio, err := stream.Next()

	if audio != nil || err != io.EOF {
		t.Fatalf("closed stream Next = (%#v, %v), want nil EOF", audio, err)
	}
}

func TestTelnyxTTSRegisterStreamAfterCloseClosesStream(t *testing.T) {
	cancelled := false
	closeCalls := 0
	provider := NewTelnyxTTS("test-key", "")
	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	stream := &telnyxTTSStream{
		cancel: func() { cancelled = true },
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if provider.registerStream(stream) {
		t.Fatal("registerStream after provider Close = true, want false")
	}
	if !cancelled {
		t.Fatal("cancel not called for stream registered after provider Close")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushText("again"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushText after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after rejected registration error = %v, want io.ErrClosedPipe", err)
	}
	if len(provider.streams) != 0 {
		t.Fatalf("provider streams = %d, want 0", len(provider.streams))
	}
}

func TestTelnyxTTSStreamAfterCloseIsRejected(t *testing.T) {
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

	provider := NewTelnyxTTS("test-key", "", WithTelnyxTTSBaseURL("wss://telnyx.test/v2/tts"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Stream(context.Background())
	if stream != nil {
		t.Fatalf("Stream after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Stream after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Stream after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestTelnyxTTSSynthesizeAfterCloseIsRejected(t *testing.T) {
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

	provider := NewTelnyxTTS("test-key", "", WithTelnyxTTSBaseURL("wss://telnyx.test/v2/tts"))
	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if stream != nil {
		t.Fatalf("Synthesize after Close stream = %#v, want nil", stream)
	}
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Synthesize after Close error = %v, want io.ErrClosedPipe", err)
	}
	if dialCalls != 0 {
		t.Fatalf("Synthesize after Close dial calls = %d, want 0", dialCalls)
	}
}

func TestTelnyxTTSAudioFromMessageDecodesBase64Audio(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{
		"audio": base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}),
	})

	audio, done, err := telnyxTTSAudioFromMessage(payload, 16000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true, want false for audio message")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 16000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 16 kHz mono", audio.Frame)
	}

	empty, done, err := telnyxTTSAudioFromMessage([]byte(`{}`), 16000)
	if err != nil {
		t.Fatalf("empty message: %v", err)
	}
	if empty != nil || !done {
		t.Fatalf("empty=%+v done=%v, want done with no audio", empty, done)
	}
}

func TestTelnyxTTSStreamDecodesReferenceMP3Audio(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	stream := &telnyxTTSStream{
		ctx:    context.Background(),
		events: make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}
	stream.startDecoder()
	defer stream.Close()

	go func() {
		stream.pushAudioData(mp3Data)
		stream.endAudioInput()
	}()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want decoded audio", err)
	}
	if audio == nil || audio.Frame == nil {
		t.Fatal("audio frame = nil, want decoded PCM frame")
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want decoded MP3 sample rate 48000", audio.Frame.SampleRate)
	}
	if audio.Frame.NumChannels != 2 {
		t.Fatalf("channels = %d, want decoded MP3 stereo", audio.Frame.NumChannels)
	}
	if len(audio.Frame.Data) == 0 {
		t.Fatal("decoded frame is empty")
	}
	if bytes.HasPrefix(mp3Data, audio.Frame.Data) {
		t.Fatal("frame data still contains raw mp3 bytes")
	}
}

func TestTelnyxTTSStreamEmitsReferenceFinalMarkerAfterMP3Decode(t *testing.T) {
	mp3Data, err := os.ReadFile(filepath.Join("..", "..", "refs", "agents", "tests", "long.mp3"))
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream := &telnyxTTSStream{
		ctx:    ctx,
		events: make(chan *tts.SynthesizedAudio, 10),
		errCh:  make(chan error, 1),
	}
	stream.startDecoder()
	defer stream.Close()

	go func() {
		stream.pushAudioData(mp3Data)
		stream.endAudioInput()
	}()

	frames := 0
	for {
		audio, err := stream.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("stream ended after %d frames without final marker", frames)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timed out after %d frames waiting for final marker", frames)
		}
		if err != nil {
			t.Fatalf("Next error = %v, want decoded audio or final marker", err)
		}
		if audio == nil {
			t.Fatal("audio = nil, want decoded audio or final marker")
		}
		if audio.IsFinal {
			if audio.Frame != nil {
				t.Fatal("final marker included frame, want boundary-only marker")
			}
			if frames == 0 {
				t.Fatal("final marker arrived before decoded MP3 frames")
			}
			return
		}
		if audio.Frame == nil {
			t.Fatal("non-final event missing decoded frame")
		}
		frames++
	}
}

func TestTelnyxTTSStreamUnexpectedCloseReturnsAPIConnectionError(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "bad audio stream"),
			time.Now().Add(time.Second),
		)
		_ = conn.Close()
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	stream := &telnyxTTSStream{
		conn:   conn,
		events: make(chan *tts.SynthesizedAudio, 1),
		errCh:  make(chan error, 1),
	}
	go stream.readLoop()

	select {
	case err := <-stream.errCh:
		var connectionErr *llm.APIConnectionError
		if !errors.As(err, &connectionErr) {
			t.Fatalf("readLoop error = %T %v, want APIConnectionError", err, err)
		}
		if !strings.Contains(err.Error(), "Telnyx TTS WebSocket closed unexpectedly") {
			t.Fatalf("readLoop error = %q, want Telnyx close context", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket close error")
	}
}

func assertTelnyxTextPayload(t *testing.T, message map[string]string, want string) {
	t.Helper()
	if got := message["text"]; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestTelnyxTTSStillImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewTelnyxTTS("test-key", "")
}
