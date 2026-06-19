package cartesia

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestCartesiaSTTDefaultsMatchReference(t *testing.T) {
	provider := NewCartesiaSTT("test-key")

	if provider.wsBaseURL != "wss://api.cartesia.ai" {
		t.Fatalf("ws base URL = %q, want reference websocket base", provider.wsBaseURL)
	}
	if provider.model != "ink-2" {
		t.Fatalf("model = %q, want ink-2", provider.model)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.audioChunkDurationMS != 160 {
		t.Fatalf("chunk duration = %d, want 160", provider.audioChunkDurationMS)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.finalTranscriptMode != "auto" {
		t.Fatalf("final transcript mode = %q, want auto", provider.finalTranscriptMode)
	}

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults {
		t.Fatalf("capabilities = %+v, want streaming interim", caps)
	}
	if caps.AlignedTranscript != "" {
		t.Fatalf("aligned transcript = %q, want empty for ink-2", caps.AlignedTranscript)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
	if got := stt.Model(provider); got != "ink-2" {
		t.Fatalf("model metadata = %q, want ink-2", got)
	}
	if got := stt.Provider(provider); got != "Cartesia" {
		t.Fatalf("provider metadata = %q, want Cartesia", got)
	}
}

func TestCartesiaSTTExposesReferenceInputSampleRate(t *testing.T) {
	provider := NewCartesiaSTT("test-key")

	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate = %d, want reference sample rate 16000", got)
	}
}

func TestCartesiaSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewCartesiaSTT("test-key", WithCartesiaSTTSampleRate(48000))

	if got := provider.InputSampleRate(); got != 48000 {
		t.Fatalf("InputSampleRate = %d, want configured sample rate 48000", got)
	}
}

func TestCartesiaSTTConstructorOptionsMatchReference(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "env-key")

	provider := NewCartesiaSTT("",
		WithCartesiaSTTEncoding("pcm_mulaw"),
	)
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.encoding != "pcm_mulaw" {
		t.Fatalf("encoding = %q, want configured encoding", provider.encoding)
	}

	streamURL, err := url.Parse(buildCartesiaSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertCartesiaQuery(t, streamURL.Query(), "encoding", "pcm_mulaw")

	provider = NewCartesiaSTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestCartesiaSTTNonEnglishDefaultsToWhisperReference(t *testing.T) {
	provider := NewCartesiaSTT("test-key", WithCartesiaSTTLanguage("es"))

	if provider.model != "ink-whisper" {
		t.Fatalf("model = %q, want ink-whisper for non-English language", provider.model)
	}
	if provider.finalTranscriptMode != "legacy" {
		t.Fatalf("final transcript mode = %q, want legacy", provider.finalTranscriptMode)
	}
	caps := provider.Capabilities()
	if caps.InterimResults {
		t.Fatal("interim results = true, want false for legacy mode")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
}

func TestCartesiaSTTUpdateOptionsMatchesReferenceFutureStreams(t *testing.T) {
	provider := NewCartesiaSTT("test-key", WithCartesiaSTTLanguage("es"))

	provider.UpdateOptions("fr-FR")

	if provider.language != "fr-FR" {
		t.Fatalf("language = %q, want updated language fr-FR", provider.language)
	}
	parsed, err := url.Parse(buildCartesiaSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	assertCartesiaQuery(t, parsed.Query(), "language", "fr")
}

func TestCartesiaSTTUpdateOptionsPropagatesLanguageToActiveStream(t *testing.T) {
	provider := NewCartesiaSTT("test-key", WithCartesiaSTTLanguage("en"))
	stream := &cartesiaSTTStream{
		state: &cartesiaSTTStreamState{language: "en", mode: "auto"},
	}
	provider.registerStream(stream)

	provider.UpdateOptions("fr-FR")

	if got := stream.state.language; got != "fr" {
		t.Fatalf("active stream language = %q, want base language fr", got)
	}
}

func TestCartesiaSTTStreamLanguageOverrideDoesNotPersistLikeReference(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	requests := make(chan *http.Request, 1)
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runCartesiaCaptureRequestWebsocketServer(serverConn, requests, closeAfterHandshake, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewCartesiaSTT("test-key",
		WithCartesiaSTTBaseURL("http://cartesia.test"),
		WithCartesiaSTTLanguage("en"),
	)
	stream, err := provider.Stream(context.Background(), "fr-FR")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()

	req := receiveCartesiaTestValue(t, requests, "websocket request")
	close(closeAfterHandshake)
	if got := req.URL.Query().Get("language"); got != "" {
		t.Fatalf("stream language query = %q, want omitted for auto endpoint", got)
	}
	providerStream, ok := stream.(*cartesiaSTTStream)
	if !ok {
		t.Fatalf("stream = %T, want *cartesiaSTTStream", stream)
	}
	if got := providerStream.state.language; got != "fr" {
		t.Fatalf("stream state language = %q, want override fr", got)
	}
	if provider.language != "en" {
		t.Fatalf("provider language = %q, want original en after stream override", provider.language)
	}
	parsed, err := url.Parse(buildCartesiaSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse provider URL: %v", err)
	}
	if got := parsed.Query().Get("language"); got != "" {
		t.Fatalf("later provider language query = %q, want omitted for auto endpoint", got)
	}
	if got := provider.languageOrDefault(); got != "en" {
		t.Fatalf("later provider default language = %q, want en", got)
	}

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket server")
	}
}

func TestCartesiaSTTOptionsBuildReferenceURLsAndHeaders(t *testing.T) {
	provider := NewCartesiaSTT("test-key",
		WithCartesiaSTTBaseURL("http://cartesia.example"),
		WithCartesiaSTTModel("ink-whisper"),
		WithCartesiaSTTLanguage("fr"),
		WithCartesiaSTTSampleRate(48000),
		WithCartesiaSTTAudioChunkDurationMS(80),
	)

	legacyURL, err := url.Parse(buildCartesiaSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse legacy URL: %v", err)
	}
	if legacyURL.String()[:len("ws://cartesia.example/stt/websocket?")] != "ws://cartesia.example/stt/websocket?" {
		t.Fatalf("legacy URL = %q, want /stt/websocket", legacyURL.String())
	}
	query := legacyURL.Query()
	assertCartesiaQuery(t, query, "model", "ink-whisper")
	assertCartesiaQuery(t, query, "sample_rate", "48000")
	assertCartesiaQuery(t, query, "encoding", "pcm_s16le")
	assertCartesiaQuery(t, query, "language", "fr")

	headers := buildCartesiaSTTHeaders(provider)
	if headers.Get("X-API-Key") != "test-key" {
		t.Fatalf("X-API-Key = %q, want key", headers.Get("X-API-Key"))
	}
	if headers.Get("Cartesia-Version") != "2025-04-16" {
		t.Fatalf("Cartesia-Version = %q, want reference version", headers.Get("Cartesia-Version"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatalf("User-Agent missing")
	}
}

func TestCartesiaSTTRequiresAPIKeyBeforeStreamRequest(t *testing.T) {
	t.Setenv("CARTESIA_API_KEY", "")
	provider := NewCartesiaSTT("", WithCartesiaSTTBaseURL("://bad-url"))

	_, err := provider.Stream(context.Background(), "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "CARTESIA_API_KEY") {
		t.Fatalf("Stream error = %q, want CARTESIA_API_KEY guidance", err)
	}
}

func TestCartesiaSTTAutoEventsMapTurnLifecycle(t *testing.T) {
	state := &cartesiaSTTStreamState{language: "en", requestID: "req-1", mode: "auto"}

	events, err := processCartesiaSTTEvent(state, map[string]any{"type": "turn.start"})
	if err != nil {
		t.Fatalf("process start: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")

	events, err = processCartesiaSTTEvent(state, map[string]any{"type": "turn.update", "transcript": "hello", "request_id": "req-2"})
	if err != nil {
		t.Fatalf("process update: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventInterimTranscript, "hello")
	if state.requestID != "req-2" {
		t.Fatalf("request id = %q, want update request id", state.requestID)
	}

	events, err = processCartesiaSTTEvent(state, map[string]any{"type": "turn.eager_end", "transcript": "hello"})
	if err != nil {
		t.Fatalf("process eager end: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventPreflightTranscript, "hello")

	events, err = processCartesiaSTTEvent(state, map[string]any{"type": "turn.resume"})
	if err != nil {
		t.Fatalf("process resume: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventInterimTranscript, "hello")

	events, err = processCartesiaSTTEvent(state, map[string]any{"type": "turn.end", "transcript": "hello done"})
	if err != nil {
		t.Fatalf("process end: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello done")
	assertCartesiaEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestCartesiaSTTLegacyEventsMapTranscriptLifecycle(t *testing.T) {
	state := &cartesiaSTTStreamState{language: "es", requestID: "req-1", mode: "legacy", speechDuration: 1.25}

	events, err := processCartesiaSTTEvent(state, map[string]any{
		"type":     "transcript",
		"text":     "hola",
		"is_final": false,
		"duration": 0.4,
		"words": []any{
			map[string]any{"word": "hola", "start": 0.1, "end": 0.3},
		},
	})
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertCartesiaEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertCartesiaEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hola")

	events, err = processCartesiaSTTEvent(state, map[string]any{
		"type":       "transcript",
		"text":       "hola final",
		"is_final":   true,
		"duration":   0.6,
		"request_id": "req-2",
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage || events[0].RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("usage event = %+v, want 1.25s usage", events[0])
	}
	assertCartesiaEvent(t, events, 1, stt.SpeechEventFinalTranscript, "hola final")
	assertCartesiaEvent(t, events, 2, stt.SpeechEventEndOfSpeech, "")
	if state.requestID != "req-2" {
		t.Fatalf("request id = %q, want req-2", state.requestID)
	}
}

func TestCartesiaSTTUnexpectedCloseFinalizesPartialAutoTranscript(t *testing.T) {
	state := &cartesiaSTTStreamState{
		language:          "en",
		requestID:         "req-1",
		mode:              "auto",
		speaking:          true,
		currentTranscript: "partial words",
		speechDuration:    1.25,
	}

	events := cartesiaSTTUnexpectedCloseEvents(state)

	if len(events) != 3 {
		t.Fatalf("events = %d, want usage, final transcript, end of speech", len(events))
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage || events[0].RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("usage event = %+v, want 1.25s usage", events[0])
	}
	assertCartesiaEvent(t, events, 1, stt.SpeechEventFinalTranscript, "partial words")
	assertCartesiaEvent(t, events, 2, stt.SpeechEventEndOfSpeech, "")
	if state.speaking {
		t.Fatal("speaking = true after unexpected close finalization, want false")
	}
	if state.currentTranscript != "" {
		t.Fatalf("current transcript = %q, want cleared", state.currentTranscript)
	}
	if state.speechDuration != 0 {
		t.Fatalf("speech duration = %v, want reset", state.speechDuration)
	}
}

func TestCartesiaSTTPushFrameBuffersReferenceAudioChunks(t *testing.T) {
	var writes [][]byte
	stream := &cartesiaSTTStream{
		state:        &cartesiaSTTStreamState{mode: "auto"},
		audioBStream: newCartesiaSTTAudioByteStream(16000, 160),
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}
	frame := func(samples int) *audiomodel.AudioFrame {
		return &audiomodel.AudioFrame{
			Data:              make([]byte, samples*2),
			SampleRate:        16000,
			NumChannels:       1,
			SamplesPerChannel: uint32(samples),
		}
	}

	if err := stream.PushFrame(frame(1280)); err != nil {
		t.Fatalf("PushFrame first half error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes after first half = %d, want 0", len(writes))
	}
	if err := stream.PushFrame(frame(1280)); err != nil {
		t.Fatalf("PushFrame second half error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 5120 {
		t.Fatalf("writes = %s, want one 160ms PCM chunk", cartesiaWriteSizes(writes))
	}
	if err := stream.PushFrame(frame(2560)); err != nil {
		t.Fatalf("PushFrame full chunk error = %v", err)
	}
	if len(writes) != 2 || len(writes[1]) != 5120 {
		t.Fatalf("writes = %s, want two 160ms PCM chunks", cartesiaWriteSizes(writes))
	}
}

func TestCartesiaSTTCloseFlushesBufferedAudioBeforeClose(t *testing.T) {
	var writes [][]byte
	var textMessages []string
	stream := &cartesiaSTTStream{
		state:        &cartesiaSTTStreamState{mode: "auto"},
		audioBStream: newCartesiaSTTAudioByteStream(16000, 160),
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
		writeText: func(data []byte) error {
			textMessages = append(textMessages, string(data))
			return nil
		},
		closeConn: func() error { return nil },
	}

	if err := stream.PushFrame(&audiomodel.AudioFrame{
		Data:              make([]byte, 1280*2),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1280,
	}); err != nil {
		t.Fatalf("PushFrame error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes before close = %s, want none", cartesiaWriteSizes(writes))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 2560 {
		t.Fatalf("writes after close = %s, want buffered 80ms chunk", cartesiaWriteSizes(writes))
	}
	if len(textMessages) != 1 || textMessages[0] != `{"type":"close"}` {
		t.Fatalf("text messages = %#v, want close message after buffered audio", textMessages)
	}
}

func TestCartesiaSTTErrorEventReportsServerErrors(t *testing.T) {
	_, err := processCartesiaSTTEvent(&cartesiaSTTStreamState{}, map[string]any{
		"type":        "error",
		"message":     "server failed",
		"status_code": float64(503),
	})
	if err == nil {
		t.Fatal("error = nil, want server error")
	}
}

func TestCartesiaSTTUnexpectedNormalCloseReturnsAPIConnectionError(t *testing.T) {
	closed := make(chan struct{})
	closeAfterHandshake := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runCartesiaNormalCloseWebsocketServer(serverConn, closeAfterHandshake, closed, serverErr)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewCartesiaSTT("test-key", WithCartesiaSTTBaseURL("http://cartesia.test"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream error = %v", err)
	}
	defer stream.Close()
	close(closeAfterHandshake)

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal websocket close")
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatalf("test websocket server error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for normal close server")
	}

	_, err = stream.Next()
	var connectionErr *llm.APIConnectionError
	if !errors.As(err, &connectionErr) {
		t.Fatalf("Next error = %T %v, want APIConnectionError", err, err)
	}
}

func cartesiaWriteSizes(writes [][]byte) string {
	sizes := make([]string, 0, len(writes))
	for _, write := range writes {
		sizes = append(sizes, fmt.Sprintf("%d", len(write)))
	}
	return strings.Join(sizes, ",")
}

func assertCartesiaQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertCartesiaEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
	if len(events[index].Alternatives) != 1 || events[index].Alternatives[0].Text != text {
		t.Fatalf("event %d alternatives = %+v, want text %q", index, events[index].Alternatives, text)
	}
}

func runCartesiaNormalCloseWebsocketServer(conn net.Conn, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	upgrader := websocket.Upgrader{}
	listener := &singleCartesiaConnListener{conn: conn}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer ws.Close()
			<-closeAfterHandshake
			err = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			close(closed)
			errCh <- err
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		errCh <- err
	}
}

func runCartesiaCaptureRequestWebsocketServer(conn net.Conn, requests chan<- *http.Request, closeAfterHandshake <-chan struct{}, closed chan<- struct{}, errCh chan<- error) {
	upgrader := websocket.Upgrader{}
	listener := &singleCartesiaConnListener{conn: conn}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests <- r.Clone(r.Context())
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				errCh <- err
				return
			}
			defer ws.Close()
			<-closeAfterHandshake
			err = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			close(closed)
			errCh <- err
		}),
	}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		errCh <- err
	}
}

func receiveCartesiaTestValue[T any](t *testing.T, ch <-chan T, label string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		var zero T
		t.Fatalf("timed out waiting for %s", label)
		return zero
	}
}

type singleCartesiaConnListener struct {
	conn net.Conn
	once sync.Once
}

func (l *singleCartesiaConnListener) Accept() (net.Conn, error) {
	var conn net.Conn
	l.once.Do(func() { conn = l.conn })
	if conn == nil {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *singleCartesiaConnListener) Close() error { return nil }

func (l *singleCartesiaConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}
