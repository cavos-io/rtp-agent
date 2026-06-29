package deepgram

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/gorilla/websocket"
)

func TestDeepgramSTTv2DefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramSTTv2("test-key")

	if provider.Label() != "deepgram.STTv2" {
		t.Fatalf("Label() = %q, want deepgram.STTv2", provider.Label())
	}
	if provider.Model() != "flux-general-en" {
		t.Fatalf("Model() = %q, want flux-general-en", provider.Model())
	}
	if provider.Provider() != "Deepgram" {
		t.Fatalf("Provider() = %q, want Deepgram", provider.Provider())
	}
	if provider.InputSampleRate() != 16000 {
		t.Fatalf("InputSampleRate() = %d, want 16000", provider.InputSampleRate())
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize || caps.AlignedTranscript != "word" {
		t.Fatalf("Capabilities() = %+v, want reference STTv2 streaming/interim/word without offline recognize", caps)
	}

	if _, err := provider.Recognize(context.Background(), nil, "en"); err == nil {
		t.Fatal("Recognize() error = nil, want reference unsupported recognize error")
	}
}

func TestDeepgramSTTv2TurnInfoEmitsReferenceEvents(t *testing.T) {
	stream := &deepgramV2Stream{
		events:   make(chan *stt.SpeechEvent, 8),
		language: "en",
	}

	events := []deepgramV2Response{
		deepgramV2Turn("StartOfTurn", "req-1", "hel", []deepgramV2Word{{Word: "hel", Start: 0.1, End: 0.2, Confidence: 0.4}}),
		deepgramV2Turn("EagerEndOfTurn", "req-1", "hello", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}}),
		deepgramV2Turn("TurnResumed", "req-1", "hello again", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}, {Word: "again", Start: 0.5, End: 0.8, Confidence: 0.9}}),
		deepgramV2Turn("EndOfTurn", "req-1", "hello again", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}, {Word: "again", Start: 0.5, End: 0.8, Confidence: 0.9}}),
	}
	for _, event := range events {
		if err := stream.processEvent(event); err != nil {
			t.Fatalf("processEvent(%s) error = %v", event.Event, err)
		}
	}

	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventPreflightTranscript,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, wantType := range wantTypes {
		got := nextDeepgramV2TestEvent(t, stream)
		if got.Type != wantType {
			t.Fatalf("event %d type = %s, want %s", i, got.Type, wantType)
		}
		if len(got.Alternatives) > 0 && got.RequestID != "req-1" {
			t.Fatalf("event %d request id = %q, want req-1", i, got.RequestID)
		}
	}
}

func TestDeepgramSTTv2StreamHandlesReferenceTurnAndClose(t *testing.T) {
	closeSeen := make(chan struct{})
	audioFrames := make(chan []byte, 2)
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramSTTv2TurnInfoWebsocketServer(serverConn, closeSeen, audioFrames, serverErr)

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

	provider := NewDeepgramSTTv2("test-key",
		WithDeepgramSTTv2BaseURL("ws://deepgram.test/v2/listen"),
		WithDeepgramSTTv2Model("flux-general-multi"),
		WithDeepgramSTTv2SampleRate(16000),
		WithDeepgramSTTv2MipOptOut(true),
	)
	stream, err := provider.Stream(context.Background(), "multi")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	timing, ok := stream.(stt.StreamTiming)
	if !ok {
		t.Fatalf("stream = %T, want StreamTiming", stream)
	}
	timing.SetStartTimeOffset(0.2)
	timing.SetStartTime(1.5)
	if timing.StartTimeOffset() != 0.2 || timing.StartTime() != 1.5 {
		t.Fatalf("timing = %v/%v, want 0.2/1.5", timing.StartTimeOffset(), timing.StartTime())
	}

	start, err := stream.Next()
	if err != nil {
		t.Fatalf("Next start error = %v", err)
	}
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("first event = %s, want start_of_speech", start.Type)
	}
	interim, err := stream.Next()
	if err != nil {
		t.Fatalf("Next interim error = %v", err)
	}
	if interim.Type != stt.SpeechEventInterimTranscript || interim.Alternatives[0].Language != "es" {
		t.Fatalf("interim = %+v, want detected-language transcript", interim)
	}
	if math.Abs(interim.Alternatives[0].StartTime-0.3) > 1e-9 || math.Abs(interim.Alternatives[0].Words[0].StartTime-0.3) > 1e-9 {
		t.Fatalf("interim timing = %+v, want start_time_offset applied", interim.Alternatives[0])
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 2000),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	select {
	case got := <-audioFrames:
		if len(got) != 1600 {
			t.Fatalf("first audio frame length = %d, want 1600", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first audio frame")
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-audioFrames:
		if len(got) != 400 {
			t.Fatalf("flush audio frame length = %d, want 400", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for flushed audio frame")
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1}}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("PushFrame after Close error = %v, want io.ErrClosedPipe", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}

	parsed, err := url.Parse(buildDeepgramSTTv2StreamURL(provider))
	if err != nil {
		t.Fatalf("parse STTv2 URL: %v", err)
	}
	if parsed.Query().Get("model") != "flux-general-multi" || parsed.Query().Get("mip_opt_out") != "true" {
		t.Fatalf("stream url query = %s, want updated model and mip_opt_out", parsed.RawQuery)
	}
}

func TestDeepgramSTTv2StreamURLUsesReferenceTurnOptions(t *testing.T) {
	provider := NewDeepgramSTTv2("test-key",
		WithDeepgramSTTv2BaseURL("https://deepgram.example/v2/listen"),
		WithDeepgramSTTv2Model("flux-general-multi"),
		WithDeepgramSTTv2SampleRate(48000),
		WithDeepgramSTTv2MipOptOut(true),
		WithDeepgramSTTv2EagerEOTThreshold(0.6),
		WithDeepgramSTTv2EOTThreshold(0.8),
		WithDeepgramSTTv2EOTTimeout(1500),
		WithDeepgramSTTv2Keyterms([]string{"LiveKit", "rtp-agent"}),
		WithDeepgramSTTv2Tags([]string{"agent", "test"}),
		WithDeepgramSTTv2LanguageHints([]string{"en", "es"}),
	)

	parsed, err := url.Parse(buildDeepgramSTTv2StreamURL(provider))
	if err != nil {
		t.Fatalf("parse STTv2 URL: %v", err)
	}
	query := parsed.Query()
	if parsed.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", parsed.Scheme)
	}
	assertDeepgramQuery(t, query, "model", "flux-general-multi")
	assertDeepgramQuery(t, query, "sample_rate", "48000")
	assertDeepgramQuery(t, query, "encoding", "linear16")
	assertDeepgramQuery(t, query, "mip_opt_out", "true")
	assertDeepgramQuery(t, query, "eager_eot_threshold", "0.6")
	assertDeepgramQuery(t, query, "eot_threshold", "0.8")
	assertDeepgramQuery(t, query, "eot_timeout_ms", "1500")
	assertDeepgramQueryValues(t, query, "keyterm", []string{"LiveKit", "rtp-agent"})
	assertDeepgramQueryValues(t, query, "tag", []string{"agent", "test"})
	assertDeepgramQueryValues(t, query, "language_hint", []string{"en", "es"})
}

func TestDeepgramSTTv2ErrorMessageReturnsAPIStatusError(t *testing.T) {
	stream := &deepgramV2Stream{}

	err := stream.processEvent(deepgramV2Response{
		Type:        "Error",
		Description: "bad turn",
	})
	if err == nil {
		t.Fatal("processEvent Error returned nil, want APIStatusError")
	}
	var statusErr *llm.APIStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("error = %T %v, want APIStatusError", err, err)
	}
	if statusErr.StatusCode != -1 {
		t.Fatalf("status error = %+v, want status -1", statusErr)
	}
}

func deepgramV2Turn(event string, requestID string, transcript string, words []deepgramV2Word) deepgramV2Response {
	return deepgramV2Response{
		Type:             "TurnInfo",
		Event:            event,
		RequestID:        requestID,
		Transcript:       transcript,
		AudioWindowStart: 0.1,
		AudioWindowEnd:   0.8,
		Words:            words,
	}
}

func nextDeepgramV2TestEvent(t *testing.T, stream *deepgramV2Stream) *stt.SpeechEvent {
	t.Helper()
	select {
	case event := <-stream.events:
		return event
	default:
		t.Fatal("missing Deepgram STTv2 event")
		return nil
	}
}

func runDeepgramSTTv2TurnInfoWebsocketServer(conn net.Conn, closeSeen chan<- struct{}, audioFrames chan<- []byte, errCh chan<- error) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		errCh <- err
		return
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", deepgramTestAcceptKey(req.Header.Get("Sec-WebSocket-Key"))); err != nil {
		errCh <- err
		return
	}
	msg := `{"type":"TurnInfo","event":"StartOfTurn","request_id":"req-v2","transcript":"hola","audio_window_start":0.1,"audio_window_end":0.4,"languages":["es"],"words":[{"word":"hola","start":0.1,"end":0.4,"confidence":0.9}]}`
	if err := writeDeepgramTestWebsocketFrame(conn, websocket.TextMessage, []byte(msg)); err != nil {
		errCh <- err
		return
	}

	for {
		opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		switch opcode {
		case websocket.BinaryMessage:
			audioFrames <- append([]byte(nil), payload...)
		case websocket.TextMessage:
			if deepgramTestWebsocketMessageType(payload) == "CloseStream" {
				close(closeSeen)
				errCh <- nil
				return
			}
		}
	}
}
