package fireworksai

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestFireworksSTTDefaultsMatchReference(t *testing.T) {
	provider := NewFireworksSTT("test-key")

	if provider.baseURL != "wss://audio-streaming.us-virginia-1.direct.fireworks.ai/v1" {
		t.Fatalf("base URL = %q, want reference websocket base URL", provider.baseURL)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.textTimeoutSeconds != 1.0 {
		t.Fatalf("text timeout = %f, want 1.0", provider.textTimeoutSeconds)
	}
	if provider.responseFormat != "verbose_json" {
		t.Fatalf("response format = %q, want verbose_json", provider.responseFormat)
	}
	if got := stt.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := stt.Provider(provider); got != "FireworksAI" {
		t.Fatalf("provider metadata = %q, want FireworksAI", got)
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

func TestFireworksSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewFireworksSTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want 16000", got)
	}
}

func TestNewFireworksSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "env-key")

	provider := NewFireworksSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewFireworksSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestFireworksSTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewFireworksSTT("test-key",
		WithFireworksBaseURL("ws://fireworks.example/v1/"),
		WithFireworksModel("whisper-v3"),
		WithFireworksLanguage("en"),
		WithFireworksPrompt("names"),
		WithFireworksTemperature(0.2),
		WithFireworksSkipVAD(true),
		WithFireworksVADKwargs(map[string]any{"threshold": 0.15}),
		WithFireworksTextTimeoutSeconds(2.5),
		WithFireworksTimestampGranularities([]string{"word", "segment"}),
	)

	streamURL, err := url.Parse(buildFireworksStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if !strings.HasPrefix(streamURL.String(), "ws://fireworks.example/v1/audio/transcriptions/streaming") {
		t.Fatalf("url = %q, want audio transcriptions streaming endpoint", streamURL.String())
	}
	if strings.Contains(streamURL.Path, "audio_streaming") {
		t.Fatalf("path = %q, want reference route without legacy audio_streaming segment", streamURL.Path)
	}
	query := streamURL.Query()
	assertFireworksQuery(t, query, "model", "whisper-v3")
	assertFireworksQuery(t, query, "language", "en")
	assertFireworksQuery(t, query, "prompt", "names")
	assertFireworksQuery(t, query, "temperature", "0.2")
	assertFireworksQuery(t, query, "skip_vad", "true")
	assertFireworksQuery(t, query, "text_timeout_seconds", "2.5")
	assertFireworksQuery(t, query, "response_format", "verbose_json")
	if got := query["timestamp_granularities"]; len(got) != 2 || got[0] != "word" || got[1] != "segment" {
		t.Fatalf("timestamp_granularities = %#v, want word/segment", got)
	}
	if got := query.Get("vad_kwargs"); !strings.Contains(got, `"threshold":0.15`) {
		t.Fatalf("vad_kwargs = %q, want encoded threshold JSON", got)
	}

	headers := buildFireworksStreamHeaders(provider)
	if headers.Get("Authorization") != "test-key" {
		t.Fatalf("Authorization = %q, want raw API key", headers.Get("Authorization"))
	}
	if headers.Get("User-Agent") != "LiveKit Agents" {
		t.Fatalf("User-Agent = %q, want LiveKit Agents", headers.Get("User-Agent"))
	}
}

func TestFireworksSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewFireworksSTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "does not support batch recognition") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestFireworksSTTPushFrameBuffersReferenceAudioChunks(t *testing.T) {
	audioCh := make(chan []byte, 2)
	closeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newFireworksSTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		for i := 0; i < 2; i++ {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if msgType != websocket.BinaryMessage {
				t.Errorf("message type = %d, want binary", msgType)
			}
			audioCh <- append([]byte(nil), payload...)
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		closeCh <- string(payload)
	})

	provider := NewFireworksSTT("test-key",
		WithFireworksBaseURL("ws://fireworks.test/v1"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: bytes.Repeat([]byte{1}, 2400)}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if got := readFireworksTestChan(t, audioCh, errCh); len(got) != 1600 {
		t.Fatalf("first audio chunk len = %d, want 1600", len(got))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if got := readFireworksTestChan(t, audioCh, errCh); len(got) != 800 {
		t.Fatalf("flush audio chunk len = %d, want 800", len(got))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if got := readFireworksTestChan(t, closeCh, errCh); got != `{"checkpoint_id":"final"}` {
		t.Fatalf("close payload = %q, want checkpoint final", got)
	}
}

func TestFireworksSTTRequiresAPIKeyBeforeStreamRequest(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "")
	provider := NewFireworksSTT("", WithFireworksBaseURL("://bad-url"))

	_, err := provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FIREWORKS_API_KEY") {
		t.Fatalf("Stream error = %q, want FIREWORKS_API_KEY guidance", err)
	}
}

func TestFireworksProcessStreamEventEmitsStartInterimFinalAndEnd(t *testing.T) {
	state := &fireworksStreamState{language: "en", lastFinalSegmentID: -1}

	events := processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{ID: 0, Text: "hello"},
		},
	}, false)

	assertFireworksEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertFireworksEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")

	events = processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{ID: 0, Text: "hello world", Words: []fireworksWord{{Word: "world", IsFinal: true}}},
		},
	}, true)

	assertFireworksEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello world")
	assertFireworksEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestFireworksProcessStreamEventKeepsNewWordsAfterFinalSegment(t *testing.T) {
	state := &fireworksStreamState{
		language:            "en",
		lastFinalSegmentID:  1,
		finalSegmentsLength: map[int]int{1: 2},
	}

	events := processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{
				ID: 1,
				Words: []fireworksWord{
					{Word: "old"},
					{Word: "words"},
					{Word: "new"},
					{Word: "tail"},
				},
			},
		},
	}, false)

	assertFireworksEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertFireworksEvent(t, events, 1, stt.SpeechEventInterimTranscript, "new tail")
}

func assertFireworksQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertFireworksEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
	if events[index].Alternatives[0].Language != "en" {
		t.Fatalf("event %d language = %q, want en", index, events[index].Alternatives[0].Language)
	}
}

func newFireworksSTTTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) FireworksSTTOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withFireworksSTTWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newFireworksSingleConnListener(serverConn)
		server := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
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

type fireworksSingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newFireworksSingleConnListener(conn net.Conn) *fireworksSingleConnListener {
	return &fireworksSingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *fireworksSingleConnListener) Accept() (net.Conn, error) {
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

func (l *fireworksSingleConnListener) Close() error {
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

func (l *fireworksSingleConnListener) Addr() net.Addr {
	return fireworksDummyAddr("pipe")
}

type fireworksDummyAddr string

func (a fireworksDummyAddr) Network() string { return string(a) }
func (a fireworksDummyAddr) String() string  { return string(a) }

func readFireworksTestChan[T any](t *testing.T, ch <-chan T, errCh <-chan error) T {
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
