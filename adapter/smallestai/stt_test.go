package smallestai

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
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

func TestSmallestAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewSmallestAISTT("test-key")

	if provider.baseURL != "https://api.smallest.ai/waves/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "pulse" {
		t.Fatalf("model = %q, want pulse", provider.model)
	}
	if got := stt.Model(provider); got != "pulse" {
		t.Fatalf("model metadata = %q, want pulse", got)
	}
	if got := stt.Provider(provider); got != "SmallestAI" {
		t.Fatalf("provider metadata = %q, want SmallestAI", got)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.encoding != "linear16" {
		t.Fatalf("encoding = %q, want linear16", provider.encoding)
	}
	if !provider.wordTimestamps {
		t.Fatal("word timestamps = false, want true")
	}
	if provider.diarize {
		t.Fatal("diarize = true, want false")
	}
	if provider.eouTimeoutMS != 0 {
		t.Fatalf("eou timeout = %d, want 0", provider.eouTimeoutMS)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestSmallestAISTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewSmallestAISTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want 16000", got)
	}
}

func TestSmallestAISTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewSmallestAISTT("test-key", WithSmallestAISTTSampleRate(48000))

	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate = %d, want 48000", got)
	}
}

func TestSmallestAISTTUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTModel("pulse"),
		WithSmallestAISTTLanguage("en"),
		WithSmallestAISTTSampleRate(16000),
		WithSmallestAISTTEncoding("linear16"),
	)

	provider.UpdateOptions(
		WithSmallestAISTTModel("pulse-v2"),
		WithSmallestAISTTLanguage("hi"),
		WithSmallestAISTTSampleRate(48000),
		WithSmallestAISTTEncoding("pcm_s16le"),
		WithSmallestAISTTEOUTimeoutMS(250),
	)

	if provider.model != "pulse-v2" {
		t.Fatalf("model = %q, want updated model", provider.model)
	}
	if provider.language != "hi" {
		t.Fatalf("language = %q, want updated language", provider.language)
	}
	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate = %d, want updated sample rate", got)
	}
	streamURL, err := url.Parse(buildSmallestAISTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "wss://api.smallest.ai/waves/v1/pulse-v2/get_text?") {
		t.Fatalf("stream URL = %q, want updated model endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertSmallestAIQuery(t, query, "language", "hi")
	assertSmallestAIQuery(t, query, "encoding", "pcm_s16le")
	assertSmallestAIQuery(t, query, "sample_rate", "48000")
	assertSmallestAIQuery(t, query, "eou_timeout_ms", "250")
}

func TestSmallestAISTTUpdateOptionsReconnectsActiveStreams(t *testing.T) {
	requestURLs := make(chan string, 2)
	handlerErr := make(chan error, 2)
	dialer := newSmallestAISTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
		select {
		case requestURLs <- r.URL.String():
		case <-time.After(time.Second):
			handlerErr <- errors.New("timed out recording websocket request URL")
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTBaseURL("ws://smallest.test/waves/v1"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	first := readSmallestAIRequestURL(t, requestURLs, handlerErr)
	if !strings.HasPrefix(first, "/waves/v1/pulse/get_text?") {
		t.Fatalf("initial request URL = %q, want pulse endpoint", first)
	}
	firstQuery, err := url.Parse(first)
	if err != nil {
		t.Fatalf("parse initial URL: %v", err)
	}
	assertSmallestAIQuery(t, firstQuery.Query(), "language", "en")
	assertSmallestAIQuery(t, firstQuery.Query(), "sample_rate", "16000")

	provider.UpdateOptions(
		WithSmallestAISTTModel("pulse-v2"),
		WithSmallestAISTTLanguage("hi"),
		WithSmallestAISTTSampleRate(48000),
		WithSmallestAISTTEncoding("pcm_s16le"),
		WithSmallestAISTTEOUTimeoutMS(250),
	)

	second := readSmallestAIRequestURL(t, requestURLs, handlerErr)
	if !strings.HasPrefix(second, "/waves/v1/pulse-v2/get_text?") {
		t.Fatalf("reconnect request URL = %q, want updated model endpoint", second)
	}
	secondQuery, err := url.Parse(second)
	if err != nil {
		t.Fatalf("parse reconnect URL: %v", err)
	}
	query := secondQuery.Query()
	assertSmallestAIQuery(t, query, "language", "hi")
	assertSmallestAIQuery(t, query, "encoding", "pcm_s16le")
	assertSmallestAIQuery(t, query, "sample_rate", "48000")
	assertSmallestAIQuery(t, query, "eou_timeout_ms", "250")
}

func TestSmallestAISTTUnexpectedNormalCloseReturnsError(t *testing.T) {
	dialer := newSmallestAISTTTestWebsocketDialer(t, func(conn *websocket.Conn, _ *http.Request) {
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(time.Second),
		)
	})

	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTBaseURL("ws://smallest.test/waves/v1"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	event, err := stream.Next()
	if err == nil {
		t.Fatalf("Next() error = nil, event = %+v, want unexpected close error", event)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = EOF, want unexpected close error")
	}
	if !strings.Contains(err.Error(), "closed unexpectedly") {
		t.Fatalf("Next() error = %v, want closed unexpectedly", err)
	}
}

func TestNewSmallestAISTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "env-key")

	provider := NewSmallestAISTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSmallestAISTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSmallestAISTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "")
	provider := NewSmallestAISTT("", WithSmallestAISTTBaseURL("://bad-url"))

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Recognize error = %q, want SMALLEST_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Stream error = %q, want SMALLEST_API_KEY guidance", err)
	}
}

func TestSmallestAISTTRecognizeRequestUsesReferenceParams(t *testing.T) {
	provider := NewSmallestAISTT("test-key")

	req, err := buildSmallestAISTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.smallest.ai/waves/v1/pulse/get_text?diarize=false&encoding=linear16&language=en&sample_rate=16000&word_timestamps=true" {
		t.Fatalf("url = %q, want reference batch endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("content type = %q, want octet stream", got)
	}
	if got := req.Header.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, []byte{0x01, 0x02}) {
		t.Fatalf("body = %#v, want audio bytes", body)
	}
}

func TestSmallestAISTTRecognizeUploadsReferenceWAV(t *testing.T) {
	var uploaded []byte
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: smallestAIRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		uploaded = body
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"transcription":"ok","language":"en"}`)),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = oldClient })

	provider := NewSmallestAISTT("test-key")
	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{
		Data:              []byte{0x01, 0x02, 0x03, 0x04},
		SampleRate:        8000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	}}, "")
	if err != nil {
		t.Fatalf("Recognize() error = %v", err)
	}

	if len(uploaded) < 48 {
		t.Fatalf("uploaded bytes = %d, want wav header plus pcm", len(uploaded))
	}
	if string(uploaded[0:4]) != "RIFF" || string(uploaded[8:12]) != "WAVE" || string(uploaded[36:40]) != "data" {
		t.Fatalf("uploaded prefix = %q/%q/%q, want RIFF/WAVE/data", uploaded[0:4], uploaded[8:12], uploaded[36:40])
	}
	if got := binary.LittleEndian.Uint32(uploaded[24:28]); got != 8000 {
		t.Fatalf("wav sample rate = %d, want 8000", got)
	}
	if got := binary.LittleEndian.Uint16(uploaded[22:24]); got != 1 {
		t.Fatalf("wav channels = %d, want 1", got)
	}
	if got := uploaded[len(uploaded)-4:]; !bytes.Equal(got, []byte{0x01, 0x02, 0x03, 0x04}) {
		t.Fatalf("wav payload tail = %#v, want original pcm", got)
	}
}

func TestSmallestAISTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTBaseURL("http://smallest.example/waves/v1/"),
		WithSmallestAISTTModel("pulse-v2"),
		WithSmallestAISTTLanguage("multi"),
		WithSmallestAISTTSampleRate(48000),
		WithSmallestAISTTEncoding("pcm_s16le"),
		WithSmallestAISTTWordTimestamps(false),
		WithSmallestAISTTDiarize(true),
		WithSmallestAISTTEOUTimeoutMS(250),
	)

	streamURL, err := url.Parse(buildSmallestAISTTStreamURL(provider, "hi"))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if !strings.HasPrefix(streamURL.String(), "ws://smallest.example/waves/v1/pulse-v2/get_text?") {
		t.Fatalf("stream URL = %q, want websocket endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertSmallestAIQuery(t, query, "language", "hi")
	assertSmallestAIQuery(t, query, "encoding", "pcm_s16le")
	assertSmallestAIQuery(t, query, "sample_rate", "48000")
	assertSmallestAIQuery(t, query, "word_timestamps", "false")
	assertSmallestAIQuery(t, query, "diarize", "true")
	assertSmallestAIQuery(t, query, "eou_timeout_ms", "250")

	headers := buildSmallestAISTTHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("X-Source") != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", headers.Get("X-Source"))
	}
}

func TestSmallestAISTTPushFrameBuffersReferenceAudioChunks(t *testing.T) {
	audioCh := make(chan []byte, 2)
	closeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	dialer := newSmallestAISTTTestWebsocketDialer(t, func(conn *websocket.Conn, r *http.Request) {
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

	provider := NewSmallestAISTT("test-key",
		WithSmallestAISTTBaseURL("ws://smallest.test/waves/v1"),
		dialer,
	)
	stream, err := provider.Stream(context.Background(), "")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: bytes.Repeat([]byte{1}, 2400)}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if got := readSmallestAITestChan(t, audioCh, errCh); len(got) != 1600 {
		t.Fatalf("first audio chunk len = %d, want 1600", len(got))
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if got := readSmallestAITestChan(t, audioCh, errCh); len(got) != 800 {
		t.Fatalf("flush audio chunk len = %d, want 800", len(got))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if got := readSmallestAITestChan(t, closeCh, errCh); got != `{"type":"close_stream"}` {
		t.Fatalf("close payload = %q, want close_stream", got)
	}
}

func TestSmallestAISTTBatchResponseMapsSpeechEvent(t *testing.T) {
	event := smallestAIBatchSpeechEvent("en", smallestAIBatchResponse{
		Transcription: "hello world",
		Language:      "en",
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.9},
			{Word: "world", Start: 0.5, End: 0.8, Confidence: 0.8},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello world" || alt.Language != "en" {
		t.Fatalf("alt = %+v, want English transcript", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.8 || alt.Confidence != 0.9 {
		t.Fatalf("timing/confidence = %+v, want first word confidence and span", alt)
	}
	if len(alt.Words) != 2 || alt.Words[0].Text != "hello" {
		t.Fatalf("words = %+v, want word timings", alt.Words)
	}
}

func TestSmallestAISTTStreamEventsMapStartInterimFinalEndAndSpeakers(t *testing.T) {
	state := &smallestAISTTStreamState{language: "multi", diarize: true}

	events := processSmallestAISTTStreamEvent(state, smallestAIStreamResponse{
		SessionID:  "session-1",
		Transcript: "hello",
		IsFinal:    false,
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.7, Speaker: intPtr(1)},
		},
	}, 1.0)

	assertSmallestAIEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertSmallestAIEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")
	if events[1].RequestID != "session-1" {
		t.Fatalf("request id = %q, want session id", events[1].RequestID)
	}

	events = processSmallestAISTTStreamEvent(state, smallestAIStreamResponse{
		SessionID:  "session-1",
		Transcript: "hello done",
		IsFinal:    true,
		Language:   "hi",
		Words: []smallestAIWord{
			{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.7, Speaker: intPtr(2)},
			{Word: "done", Start: 0.5, End: 0.9, Confidence: 0.8, Speaker: intPtr(2)},
		},
	}, 0.5)

	assertSmallestAIEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello done")
	assertSmallestAIEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
	alt := events[0].Alternatives[0]
	if alt.Language != "hi" {
		t.Fatalf("language = %q, want detected language", alt.Language)
	}
	if alt.SpeakerID != "S2" {
		t.Fatalf("speaker id = %q, want S2", alt.SpeakerID)
	}
	if alt.StartTime != 0.6 || alt.EndTime != 1.4 {
		t.Fatalf("time range = %v-%v, want offset word timings", alt.StartTime, alt.EndTime)
	}
}

func assertSmallestAIQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertSmallestAIEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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

func intPtr(v int) *int {
	return &v
}

func newSmallestAISTTTestWebsocketDialer(t *testing.T, handler func(*websocket.Conn, *http.Request)) SmallestAISTTOption {
	t.Helper()
	upgrader := websocket.Upgrader{}
	return withSmallestAISTTWebsocketDialer(func(ctx context.Context, endpoint string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		clientConn, serverConn := net.Pipe()
		listener := newSmallestAISingleConnListener(serverConn)
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

type smallestAISingleConnListener struct {
	mu     sync.Mutex
	once   sync.Once
	conn   net.Conn
	closed chan struct{}
}

func newSmallestAISingleConnListener(conn net.Conn) *smallestAISingleConnListener {
	return &smallestAISingleConnListener{
		conn:   conn,
		closed: make(chan struct{}),
	}
}

func (l *smallestAISingleConnListener) Accept() (net.Conn, error) {
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

func (l *smallestAISingleConnListener) Close() error {
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

func (l *smallestAISingleConnListener) Addr() net.Addr {
	return smallestAIDummyAddr("pipe")
}

type smallestAIDummyAddr string

func (a smallestAIDummyAddr) Network() string { return string(a) }
func (a smallestAIDummyAddr) String() string  { return string(a) }

type smallestAIRoundTripFunc func(*http.Request) (*http.Response, error)

func (f smallestAIRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func readSmallestAITestChan[T any](t *testing.T, ch <-chan T, errCh <-chan error) T {
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

func readSmallestAIRequestURL(t *testing.T, ch <-chan string, errCh <-chan error) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for websocket request")
	}
	return ""
}

func TestSmallestAISTTRecognizeResponseDecode(t *testing.T) {
	body := `{"transcription":"ok","language":"en","words":[{"word":"ok","start":0,"end":0.2,"confidence":0.5}]}`
	var resp smallestAIBatchResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Transcription != "ok" || len(resp.Words) != 1 {
		t.Fatalf("response = %+v, want decoded batch response", resp)
	}
}
