package gradium

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
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
	audio := receiveGradiumMessage(t, audioCh, "audio")
	if audio["type"] != "audio" || audio["audio"] != base64.StdEncoding.EncodeToString([]byte{0x01, 0x02}) {
		t.Fatalf("audio = %#v, want base64 audio message", audio)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	closeMsg := receiveGradiumMessage(t, closeCh, "close")
	if closeMsg["terminate_session"] != true {
		t.Fatalf("close = %#v, want terminate session", closeMsg)
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
