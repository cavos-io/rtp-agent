package gradium

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestGradiumSTTDefaultsMatchReference(t *testing.T) {
	provider := NewGradiumSTT("test-key")

	if provider.modelEndpoint != "wss://api.gradium.ai/api/speech/asr" {
		t.Fatalf("model endpoint = %q, want reference ASR endpoint", provider.modelEndpoint)
	}
	if provider.modelName != "default" {
		t.Fatalf("model name = %q, want default", provider.modelName)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.bufferSizeSeconds != 0.08 {
		t.Fatalf("buffer size = %f, want 0.08", provider.bufferSizeSeconds)
	}
	if provider.vadThreshold != 0.9 {
		t.Fatalf("vad threshold = %f, want 0.9", provider.vadThreshold)
	}
	if provider.vadBucket == nil || *provider.vadBucket != 2 {
		t.Fatalf("vad bucket = %#v, want 2", provider.vadBucket)
	}
	if !provider.vadFlush {
		t.Fatal("vad flush = false, want true")
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.Label() != "gradium.STT" {
		t.Fatalf("label = %q, want gradium.STT", provider.Label())
	}
	if got := stt.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := stt.Provider(provider); got != "Gradium" {
		t.Fatalf("provider metadata = %q, want Gradium", got)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestGradiumSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewGradiumSTT("test-key")

	if got := provider.InputSampleRate(); got != 24000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 24000", got)
	}
}

func TestNewGradiumSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "env-key")

	provider := NewGradiumSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewGradiumSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestGradiumSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GRADIUM_API_KEY", "")
	provider := NewGradiumSTT("", WithGradiumSTTModelEndpoint("://bad-url"))

	_, err := provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GRADIUM_API_KEY") {
		t.Fatalf("Stream error = %q, want GRADIUM_API_KEY guidance", err)
	}
}

func TestGradiumSTTOptionsBuildReferenceSetupAndHeaders(t *testing.T) {
	temp := 0.2
	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("wss://gradium.example/asr"),
		WithGradiumSTTModelName("custom"),
		WithGradiumSTTLanguage("fr"),
		WithGradiumSTTTemperature(temp),
		WithGradiumSTTVADBucket(nil),
		WithGradiumSTTVADFlush(false),
		WithGradiumSTTBufferSizeSeconds(0.16),
	)

	setup := buildGradiumSTTSetup(provider)
	assertGradiumSTTSetup(t, setup, "type", "setup")
	assertGradiumSTTSetup(t, setup, "model_name", "custom")
	assertGradiumSTTSetup(t, setup, "input_format", "pcm")
	config := setup["json_config"].(map[string]any)
	assertGradiumSTTSetup(t, config, "language", "fr")
	if config["temp"] != 0.2 {
		t.Fatalf("temp = %#v, want 0.2", config["temp"])
	}
	if provider.modelEndpoint != "wss://gradium.example/asr" {
		t.Fatalf("model endpoint = %q, want custom endpoint", provider.modelEndpoint)
	}
	if provider.vadBucket != nil {
		t.Fatalf("vad bucket = %#v, want nil", provider.vadBucket)
	}
	if provider.vadFlush {
		t.Fatal("vad flush = true, want false")
	}
	if provider.bufferSizeSeconds != 0.16 {
		t.Fatalf("buffer size = %f, want 0.16", provider.bufferSizeSeconds)
	}

	headers := buildGradiumSTTHeaders(provider)
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", headers.Get("x-api-key"))
	}
	if headers.Get("x-api-source") != "livekit" {
		t.Fatalf("x-api-source = %q, want livekit", headers.Get("x-api-source"))
	}
}

func TestGradiumSTTAudioAndCloseMessagesMatchReference(t *testing.T) {
	audioMsg := buildGradiumSTTAudioMessage([]byte{0x01, 0x02})
	assertGradiumSTTSetup(t, audioMsg, "type", "audio")
	if audioMsg["audio"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio = %q, want base64 pcm", audioMsg["audio"])
	}

	closeMsg := buildGradiumSTTCloseMessage()
	if closeMsg["terminate_session"] != true {
		t.Fatalf("close message = %#v, want terminate_session true", closeMsg)
	}
}

func TestGradiumSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewGradiumSTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestGradiumSTTStreamLanguageOverrideDoesNotMutateDefault(t *testing.T) {
	setupCh := make(chan map[string]any, 2)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		_, setupPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		setupCh <- decodeGradiumMessage(t, setupPayload)
		_, _, _ = conn.ReadMessage()
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		WithGradiumSTTLanguage("en"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "id")
	if err != nil {
		t.Fatalf("first Stream returned error: %v", err)
	}
	firstSetup := receiveGradiumMessage(t, setupCh, "first setup")
	firstConfig := firstSetup["json_config"].(map[string]any)
	if firstConfig["language"] != "id" {
		t.Fatalf("first setup language = %#v, want id", firstConfig["language"])
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}

	stream, err = provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("second Stream returned error: %v", err)
	}
	defer stream.Close()
	secondSetup := receiveGradiumMessage(t, setupCh, "second setup")
	secondConfig := secondSetup["json_config"].(map[string]any)
	if secondConfig["language"] != "en" {
		t.Fatalf("second setup language = %#v, want configured default en", secondConfig["language"])
	}
	if provider.language != "en" {
		t.Fatalf("provider language = %q, want unchanged en", provider.language)
	}
}

func TestGradiumSTTStreamSendsSetupAudioAndCloseMessages(t *testing.T) {
	setupCh := make(chan map[string]any, 1)
	audioCh := make(chan map[string]any, 1)
	closeCh := make(chan map[string]any, 1)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		_, setupPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		setupCh <- decodeGradiumMessage(t, setupPayload)

		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"text","text":"hello","start_s":0}`)); err != nil {
			t.Errorf("write text event: %v", err)
			return
		}

		_, audioPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read audio: %v", err)
			return
		}
		audioCh <- decodeGradiumMessage(t, audioPayload)

		_, closePayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read close: %v", err)
			return
		}
		closeCh <- decodeGradiumMessage(t, closePayload)
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "id")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	setup := receiveGradiumMessage(t, setupCh, "setup")
	if setup["type"] != "setup" {
		t.Fatalf("setup = %#v, want setup message", setup)
	}
	config := setup["json_config"].(map[string]any)
	if config["language"] != "id" {
		t.Fatalf("setup language = %#v, want id", config["language"])
	}

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = %v, want start of speech", event.Type)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || event.Alternatives[0].Text != "hello" {
		t.Fatalf("second event = %#v, want interim hello", event)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}}); err != nil {
		t.Fatalf("PushFrame returned error: %v", err)
	}
	assertNoGradiumMessage(t, audioCh, "partial audio before flush")
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	audio := receiveGradiumMessage(t, audioCh, "audio")
	if audio["type"] != "audio" || audio["audio"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio = %#v, want base64 audio message", audio)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	closeMsg := receiveGradiumMessage(t, closeCh, "close")
	if closeMsg["terminate_session"] != true {
		t.Fatalf("close = %#v, want terminate session", closeMsg)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x03, 0x04}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after Close error = %v, want io.ErrClosedPipe", err)
	}
}

func TestGradiumSTTStreamAppliesReferenceStartTimeOffset(t *testing.T) {
	writeTranscript := make(chan struct{})
	errCh := make(chan error, 1)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			errCh <- err
			return
		}
		select {
		case <-writeTranscript:
		case <-time.After(time.Second):
			errCh <- errors.New("timed out waiting to write transcript")
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"text","text":"hello","start_s":0.25}`)); err != nil {
			errCh <- err
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatal("gradium STT stream does not implement stt.StreamTiming")
	}
	timing.SetStartTimeOffset(2.5)
	timing.SetStartTime(123.5)
	if timing.StartTimeOffset() != 2.5 || timing.StartTime() != 123.5 {
		t.Fatalf("timing = offset %v start %v, want reference values", timing.StartTimeOffset(), timing.StartTime())
	}
	close(writeTranscript)

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = %v, want start of speech", event.Type)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("second Next returned error: %v", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("second event = %#v, want interim transcript", event)
	}
	alt := event.Alternatives[0]
	if alt.StartTime != 2.75 {
		t.Fatalf("transcript start time = %v, want reference start_time_offset applied", alt.StartTime)
	}
	select {
	case err := <-errCh:
		t.Fatalf("websocket server: %v", err)
	default:
	}

	assertGradiumPanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		timing.SetStartTimeOffset(-0.01)
	})
	if got := timing.StartTimeOffset(); got != 2.5 {
		t.Fatalf("StartTimeOffset after rejected update = %v, want 2.5", got)
	}
	assertGradiumPanicsWithMessage(t, "start_time must be non-negative", func() {
		timing.SetStartTime(-0.01)
	})
	if got := timing.StartTime(); got != 123.5 {
		t.Fatalf("StartTime after rejected update = %v, want 123.5", got)
	}
}

func TestGradiumSTTClosedStreamNextReturnsEOF(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := &gradiumSTTStream{
		ctx:    ctx,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error),
		closed: true,
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript}

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil", event)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next error = %v, want io.EOF", err)
	}
}

func TestGradiumSTTPendingNextReturnsEOFAfterClose(t *testing.T) {
	ctx := newGradiumControlledCancelContext()
	stream := &gradiumSTTStream{
		ctx:    ctx,
		events: make(chan *stt.SpeechEvent),
		errCh:  make(chan error),
	}
	resultCh := make(chan error, 1)
	go func() {
		_, err := stream.Next()
		resultCh <- err
	}()
	<-ctx.doneObserved

	stream.mu.Lock()
	stream.closed = true
	stream.mu.Unlock()
	ctx.cancel()

	select {
	case err := <-resultCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("pending Next after Close error = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending Next after Close")
	}
}

func TestGradiumSTTUnexpectedNormalCloseReturnsReferenceError(t *testing.T) {
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second)); err != nil {
			t.Errorf("write close: %v", err)
		}
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	event, err := stream.Next()
	if event != nil {
		t.Fatalf("Next event = %#v, want nil on provider close", event)
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Next error = %v, want reference provider status error", err)
	}
}

func TestGradiumSTTNextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	for range 64 {
		stream := &gradiumSTTStream{
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
			ctx:    context.Background(),
		}
		stream.events <- &stt.SpeechEvent{
			Type: stt.SpeechEventFinalTranscript,
			Alternatives: []stt.SpeechData{{
				Text: "hello",
			}},
		}
		stream.errCh <- errors.New("provider closed after transcript")

		event, err := stream.Next()
		if err != nil {
			t.Fatalf("Next error = %v, want queued transcript before stream error", err)
		}
		if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello" {
			t.Fatalf("Next event = %#v, want queued final transcript", event)
		}
	}
}

type gradiumReadTimeoutError struct{}

func (gradiumReadTimeoutError) Error() string   { return "read timeout" }
func (gradiumReadTimeoutError) Timeout() bool   { return true }
func (gradiumReadTimeoutError) Temporary() bool { return true }

func TestGradiumSTTReadTimeoutDoesNotAbortReferenceStream(t *testing.T) {
	messages := [][]byte{
		[]byte(`{"type":"text","text":"after timeout","start_s":0.25}`),
	}
	releaseRead := make(chan struct{})
	reads := 0
	stream := &gradiumSTTStream{
		events: make(chan *stt.SpeechEvent, 2),
		errCh:  make(chan error, 1),
		ctx:    context.Background(),
		state:  &gradiumSTTMessageState{language: "en"},
		readMessage: func() (int, []byte, error) {
			reads++
			if reads == 1 {
				return 0, nil, gradiumReadTimeoutError{}
			}
			if reads-2 < len(messages) {
				return websocket.TextMessage, messages[reads-2], nil
			}
			<-releaseRead
			return 0, nil, io.EOF
		},
	}

	go stream.readLoop(nil)
	t.Cleanup(func() {
		close(releaseRead)
	})

	event, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v, want start after timeout", err)
	}
	if event.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event.Type = %s, want start_of_speech", event.Type)
	}
	event, err = stream.Next()
	if err != nil {
		t.Fatalf("transcript Next error = %v, want transcript after timeout", err)
	}
	if event.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event.Type = %s, want interim transcript", event.Type)
	}
	if event.Alternatives[0].Text != "after timeout" {
		t.Fatalf("text = %q, want transcript after timeout", event.Alternatives[0].Text)
	}
}

func TestGradiumSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	closeNow := make(chan struct{})
	closed := make(chan struct{})
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		<-closeNow
		_ = conn.UnderlyingConn().Close()
		close(closed)
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	gradiumStream, ok := stream.(*gradiumSTTStream)
	if !ok {
		t.Fatalf("stream type = %T, want *gradiumSTTStream", stream)
	}
	close(closeNow)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("server did not close websocket")
	}

	frame := &model.AudioFrame{Data: gradiumBytesOfLength(3840, 0x11)}
	for i := 0; i < 3; i++ {
		if err = stream.PushFrame(frame); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("PushFrame after server close error = nil, want write failure")
	}
	if !gradiumStream.isClosed() {
		t.Fatal("stream remains open after audio write failure")
	}
	if err := stream.PushFrame(frame); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Flush(); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Flush after write failure error = %v, want io.ErrClosedPipe", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v", err)
	}
}

func newGradiumSTTTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) GradiumSTTOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withGradiumSTTWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newGradiumSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("Upgrade returned error: %v", err)
					return
				}
				defer conn.Close()
				handler(conn, r)
			}),
		}
		serverErrCh := make(chan error, 1)
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				serverErrCh <- err
			}
		}()
		t.Cleanup(func() {
			_ = server.Close()
			_ = listener.Close()
			_ = clientConn.Close()
			_ = serverConn.Close()
		})

		dialer := websocket.Dialer{
			NetDialContext: func(context.Context, string, string) (net.Conn, error) {
				return clientConn, nil
			},
		}
		conn, response, err := dialer.DialContext(ctx, endpoint, headers)
		select {
		case serverErr := <-serverErrCh:
			if err == nil {
				err = serverErr
			}
		default:
		}
		return conn, response, err
	})
}

type gradiumSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newGradiumSingleConnListener(conn net.Conn) *gradiumSingleConnListener {
	return &gradiumSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *gradiumSingleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.conn != nil {
		conn := l.conn
		l.conn = nil
		l.mu.Unlock()
		return conn, nil
	}
	l.mu.Unlock()

	<-l.closed
	return nil, net.ErrClosed
}

func (l *gradiumSingleConnListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.mu.Lock()
		if l.conn != nil {
			_ = l.conn.Close()
			l.conn = nil
		}
		l.mu.Unlock()
	})
	return nil
}

func (l *gradiumSingleConnListener) Addr() net.Addr {
	return gradiumDummyAddr("pipe")
}

type gradiumDummyAddr string

func (a gradiumDummyAddr) Network() string { return string(a) }
func (a gradiumDummyAddr) String() string  { return string(a) }

func TestGradiumSTTPushFrameBuffersReferenceAudioChunks(t *testing.T) {
	audioCh := make(chan map[string]any, 2)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read audio %d: %v", i, err)
				return
			}
			audioCh <- decodeGradiumMessage(t, payload)
		}
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	first := gradiumBytesOfLength(3838, 0x01)
	second := []byte{0x02, 0x03}
	if err := stream.PushFrame(&model.AudioFrame{Data: first}); err != nil {
		t.Fatalf("first PushFrame returned error: %v", err)
	}
	assertNoGradiumMessage(t, audioCh, "incomplete reference chunk")
	if err := stream.PushFrame(&model.AudioFrame{Data: second}); err != nil {
		t.Fatalf("second PushFrame returned error: %v", err)
	}
	firstAudio := receiveGradiumMessage(t, audioCh, "full chunk audio")
	wantFirst := append(append([]byte{}, first...), second...)
	if firstAudio["type"] != "audio" || firstAudio["audio"] != base64.StdEncoding.EncodeToString(wantFirst) {
		t.Fatalf("first audio = %#v, want one 3840-byte reference chunk", firstAudio)
	}

	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x04, 0x05}}); err != nil {
		t.Fatalf("third PushFrame returned error: %v", err)
	}
	assertNoGradiumMessage(t, audioCh, "partial chunk before flush")
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	flushed := receiveGradiumMessage(t, audioCh, "flushed audio")
	if flushed["type"] != "audio" || flushed["audio"] != base64.StdEncoding.EncodeToString([]byte{0x04, 0x05}) {
		t.Fatalf("flushed audio = %#v, want trailing partial chunk", flushed)
	}
}

func TestGradiumSTTPushFrameHonorsReferenceBufferSizeOption(t *testing.T) {
	audioCh := make(chan map[string]any, 1)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read audio: %v", err)
			return
		}
		audioCh <- decodeGradiumMessage(t, payload)
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		WithGradiumSTTBufferSizeSeconds(0.16),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	first := gradiumBytesOfLength(7678, 0x01)
	second := []byte{0x02, 0x03}
	if err := stream.PushFrame(&model.AudioFrame{Data: first}); err != nil {
		t.Fatalf("first PushFrame returned error: %v", err)
	}
	assertNoGradiumMessage(t, audioCh, "incomplete configured reference chunk")
	if err := stream.PushFrame(&model.AudioFrame{Data: second}); err != nil {
		t.Fatalf("second PushFrame returned error: %v", err)
	}
	audioMsg := receiveGradiumMessage(t, audioCh, "configured buffer audio")
	want := append(append([]byte{}, first...), second...)
	if audioMsg["type"] != "audio" || audioMsg["audio"] != base64.StdEncoding.EncodeToString(want) {
		t.Fatalf("audio = %#v, want one 7680-byte configured reference chunk", audioMsg)
	}
}

func TestGradiumSTTUpdateOptionsReconnectsActiveStreamBuffer(t *testing.T) {
	setupCh := make(chan map[string]any, 2)
	audioCh := make(chan map[string]any, 1)
	var connMu sync.Mutex
	connIndex := 0
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		connMu.Lock()
		connIndex++
		index := connIndex
		connMu.Unlock()

		_, setupPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read setup %d: %v", index, err)
			return
		}
		setupCh <- decodeGradiumMessage(t, setupPayload)

		if index == 1 {
			_, _, _ = conn.ReadMessage()
			return
		}

		_, audioPayload, err := conn.ReadMessage()
		if err != nil {
			t.Errorf("read reconnected audio: %v", err)
			return
		}
		audioCh <- decodeGradiumMessage(t, audioPayload)
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()
	_ = receiveGradiumMessage(t, setupCh, "initial setup")

	provider.UpdateOptions(WithGradiumSTTUpdateBufferSizeSeconds(0.16))
	if err := stream.PushFrame(&model.AudioFrame{Data: gradiumBytesOfLength(7678, 0x01)}); err != nil {
		t.Fatalf("first PushFrame returned error: %v", err)
	}
	assertNoGradiumMessage(t, audioCh, "incomplete reconnected reference chunk")
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x02, 0x03}}); err != nil {
		t.Fatalf("second PushFrame returned error: %v", err)
	}
	_ = receiveGradiumMessage(t, setupCh, "reconnect setup")
	audioMsg := receiveGradiumMessage(t, audioCh, "reconnected configured buffer audio")
	if audioMsg["type"] != "audio" {
		t.Fatalf("reconnected message = %#v, want audio", audioMsg)
	}
}

func TestGradiumSTTPositiveVADFlushesReferenceSilence(t *testing.T) {
	audioCh := make(chan map[string]any, 6)
	dialer := newGradiumSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read setup: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"text","text":"hello","start_s":0}`)); err != nil {
			t.Errorf("write text: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"step","vad":[{}, {}, {"inactivity_prob":0.95}]}`)); err != nil {
			t.Errorf("write vad step: %v", err)
			return
		}
		for i := 0; i < 6; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read silence audio %d: %v", i, err)
				return
			}
			audioCh <- decodeGradiumMessage(t, payload)
		}
	})

	provider := NewGradiumSTT("test-key",
		WithGradiumSTTModelEndpoint("ws://gradium.test/asr"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	defer stream.Close()

	for i := 0; i < 6; i++ {
		audio := receiveGradiumMessage(t, audioCh, "vad silence audio")
		if audio["type"] != "audio" || audio["audio"] != base64.StdEncoding.EncodeToString(make([]byte, 3840)) {
			t.Fatalf("silence audio %d = %#v, want one 1920-sample zero frame", i, audio)
		}
	}
}

func TestGradiumSTTProcessMessagesMapsTextAndVADFinal(t *testing.T) {
	bucket := 2
	state := &gradiumSTTMessageState{language: "en", vadBucket: &bucket, vadThreshold: 0.9, delayInTokens: 1}

	events, err := processGradiumSTTMessage(state, []byte(`{"type":"text","text":"hello","start_s":1.25}`), 0.5)
	if err != nil {
		t.Fatalf("process text: %v", err)
	}
	assertGradiumSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertGradiumSTTEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	if events[1].Alternatives[0].StartTime != 1.75 {
		t.Fatalf("start time = %f, want 1.75", events[1].Alternatives[0].StartTime)
	}

	events, err = processGradiumSTTMessage(state, []byte(`{"type":"step","vad":[{}, {}, {"inactivity_prob":0.95}]}`), 0)
	if err != nil {
		t.Fatalf("process first vad step: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no events until delay expires", events)
	}

	events, err = processGradiumSTTMessage(state, []byte(`{"type":"step","vad":[{}, {}, {"inactivity_prob":0.95}]}`), 0)
	if err != nil {
		t.Fatalf("process final vad step: %v", err)
	}
	assertGradiumSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello")
	assertGradiumSTTEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestGradiumSTTProcessMessageIgnoresReferenceMalformedTextFrame(t *testing.T) {
	state := &gradiumSTTMessageState{language: "en"}
	events, err := processGradiumSTTMessage(state, []byte(`not-json`), 0)
	if err != nil {
		t.Fatalf("malformed text frame error = %v, want nil", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want ignored malformed frame", events)
	}
}

func TestGradiumSTTZeroVADBucketDisablesFinalizationLikeReference(t *testing.T) {
	bucket := 0
	state := &gradiumSTTMessageState{language: "en", vadBucket: &bucket, vadThreshold: 0.9, delayInTokens: 1}

	if _, err := processGradiumSTTMessage(state, []byte(`{"type":"text","text":"hello","start_s":0}`), 0); err != nil {
		t.Fatalf("process text: %v", err)
	}

	events, err := processGradiumSTTMessage(state, []byte(`{"type":"step","vad":[{"inactivity_prob":0.95}]}`), 0)
	if err != nil {
		t.Fatalf("process first vad step: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want no events when reference treats vad_bucket=0 as disabled", events)
	}
	if state.remainingSteps != nil {
		t.Fatalf("remainingSteps = %d, want nil when vad_bucket=0 disables VAD", *state.remainingSteps)
	}
}

func TestGradiumSTTProcessReadyUpdatesTimingDefaults(t *testing.T) {
	state := &gradiumSTTMessageState{}
	_, err := processGradiumSTTMessage(state, []byte(`{"type":"ready","delay_in_tokens":9,"frame_size":960}`), 0)
	if err != nil {
		t.Fatalf("process ready: %v", err)
	}
	if state.delayInTokens != 9 {
		t.Fatalf("delay = %d, want 9", state.delayInTokens)
	}
	if state.frameSize != 960 {
		t.Fatalf("frame size = %d, want 960", state.frameSize)
	}
}

func assertGradiumSTTSetup(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		encoded, _ := json.Marshal(payload)
		t.Fatalf("%s = %#v, want %q in %s", key, got, want, encoded)
	}
}

func decodeGradiumMessage(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode websocket payload %q: %v", string(payload), err)
	}
	return message
}

func receiveGradiumMessage(t *testing.T, ch <-chan map[string]any, label string) map[string]any {
	t.Helper()
	select {
	case message := <-ch:
		return message
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s message", label)
		return nil
	}
}

func assertNoGradiumMessage(t *testing.T, ch <-chan map[string]any, label string) {
	t.Helper()
	select {
	case message := <-ch:
		t.Fatalf("received %s message before expected: %#v", label, message)
	case <-time.After(25 * time.Millisecond):
	}
}

func gradiumBytesOfLength(length int, value byte) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = value
	}
	return data
}

type gradiumControlledCancelContext struct {
	context.Context
	done         chan struct{}
	doneObserved chan struct{}
}

func newGradiumControlledCancelContext() *gradiumControlledCancelContext {
	return &gradiumControlledCancelContext{
		Context:      context.Background(),
		done:         make(chan struct{}),
		doneObserved: make(chan struct{}),
	}
}

func (c *gradiumControlledCancelContext) Done() <-chan struct{} {
	select {
	case <-c.doneObserved:
	default:
		close(c.doneObserved)
	}
	return c.done
}

func (c *gradiumControlledCancelContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *gradiumControlledCancelContext) cancel() {
	close(c.done)
}

func assertGradiumSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event %d type = %v, want %v", index, events[index].Type, eventType)
	}
	if text == "" {
		return
	}
	if len(events[index].Alternatives) != 1 {
		t.Fatalf("event %d alternatives = %d, want 1", index, len(events[index].Alternatives))
	}
	if events[index].Alternatives[0].Text != text {
		t.Fatalf("event %d text = %q, want %q", index, events[index].Alternatives[0].Text, text)
	}
}

func assertGradiumPanicsWithMessage(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatalf("function did not panic, want %q", want)
		}
		if got := fmt.Sprint(recovered); got != want {
			t.Fatalf("panic = %q, want %q", got, want)
		}
	}()
	fn()
}
