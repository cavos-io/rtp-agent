package deepgram

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
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

func TestDeepgramSTTv2TurnResumedStartsReferenceSpeech(t *testing.T) {
	stream := &deepgramV2Stream{
		events:   make(chan *stt.SpeechEvent, 8),
		language: "en",
	}

	events := []deepgramV2Response{
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

func TestDeepgramSTTv2TurnUpdatesStartReferenceSpeech(t *testing.T) {
	tests := []struct {
		name      string
		event     string
		wantEvent stt.SpeechEventType
	}{
		{name: "update", event: "Update", wantEvent: stt.SpeechEventInterimTranscript},
		{name: "eager_end", event: "EagerEndOfTurn", wantEvent: stt.SpeechEventPreflightTranscript},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &deepgramV2Stream{
				events:   make(chan *stt.SpeechEvent, 8),
				language: "en",
			}

			resp := deepgramV2Turn(tt.event, "req-1", "hello", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}})
			if err := stream.processEvent(resp); err != nil {
				t.Fatalf("processEvent(%s) error = %v", tt.event, err)
			}

			start := nextDeepgramV2TestEvent(t, stream)
			if start.Type != stt.SpeechEventStartOfSpeech {
				t.Fatalf("event 0 type = %s, want %s", start.Type, stt.SpeechEventStartOfSpeech)
			}
			transcript := nextDeepgramV2TestEvent(t, stream)
			if transcript.Type != tt.wantEvent {
				t.Fatalf("event 1 type = %s, want %s", transcript.Type, tt.wantEvent)
			}
			if len(transcript.Alternatives) != 1 || transcript.Alternatives[0].Text != "hello" {
				t.Fatalf("alternatives = %+v, want transcript hello", transcript.Alternatives)
			}
		})
	}
}

func TestDeepgramSTTv2DuplicateStartPreservesReferenceTranscript(t *testing.T) {
	stream := &deepgramV2Stream{
		events:   make(chan *stt.SpeechEvent, 8),
		language: "en",
	}

	events := []deepgramV2Response{
		deepgramV2Turn("StartOfTurn", "req-1", "hel", []deepgramV2Word{{Word: "hel", Start: 0.1, End: 0.2, Confidence: 0.4}}),
		deepgramV2Turn("StartOfTurn", "req-1", "hello", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}}),
		deepgramV2Turn("EndOfTurn", "req-1", "hello", []deepgramV2Word{{Word: "hello", Start: 0.1, End: 0.4, Confidence: 0.8}}),
	}
	for _, event := range events {
		if err := stream.processEvent(event); err != nil {
			t.Fatalf("processEvent(%s) error = %v", event.Event, err)
		}
	}

	wantTypes := []stt.SpeechEventType{
		stt.SpeechEventStartOfSpeech,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventInterimTranscript,
		stt.SpeechEventFinalTranscript,
		stt.SpeechEventEndOfSpeech,
	}
	for i, wantType := range wantTypes {
		got := nextDeepgramV2TestEvent(t, stream)
		if got.Type != wantType {
			t.Fatalf("event %d type = %s, want %s", i, got.Type, wantType)
		}
		if got.Type == stt.SpeechEventInterimTranscript && got.Alternatives[0].Text == "hello" {
			return
		}
	}
	t.Fatal("duplicate StartOfTurn transcript was not emitted")
}

func TestDeepgramSTTv2TurnInfoPreservesReferenceTranscriptWithoutWords(t *testing.T) {
	stream := &deepgramV2Stream{
		events:   make(chan *stt.SpeechEvent, 8),
		language: "en",
	}

	resp := deepgramV2Turn("EndOfTurn", "req-1", "hello without words", nil)
	resp.AudioWindowStart = 0.2
	resp.AudioWindowEnd = 0.9
	if err := stream.processEvent(resp); err != nil {
		t.Fatalf("processEvent(EndOfTurn) error = %v", err)
	}

	start := nextDeepgramV2TestEvent(t, stream)
	if start.Type != stt.SpeechEventStartOfSpeech {
		t.Fatalf("event 0 type = %s, want %s", start.Type, stt.SpeechEventStartOfSpeech)
	}
	final := nextDeepgramV2TestEvent(t, stream)
	if final.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event 1 type = %s, want %s", final.Type, stt.SpeechEventFinalTranscript)
	}
	if len(final.Alternatives) != 1 || final.Alternatives[0].Text != "hello without words" {
		t.Fatalf("final alternatives = %+v, want transcript without word timings", final.Alternatives)
	}
	if final.Alternatives[0].StartTime != 0.2 || final.Alternatives[0].EndTime != 0.9 {
		t.Fatalf("final timing = %.1f..%.1f, want audio window", final.Alternatives[0].StartTime, final.Alternatives[0].EndTime)
	}
	end := nextDeepgramV2TestEvent(t, stream)
	if end.Type != stt.SpeechEventEndOfSpeech {
		t.Fatalf("event 2 type = %s, want %s", end.Type, stt.SpeechEventEndOfSpeech)
	}
}

func TestDeepgramSTTv2StreamRejectsNegativeTimingAnchors(t *testing.T) {
	stream := &deepgramV2Stream{}
	assertDeepgramPanicsWithMessage(t, "start_time_offset must be non-negative", func() {
		stream.SetStartTimeOffset(-0.01)
	})
	assertDeepgramPanicsWithMessage(t, "start_time must be non-negative", func() {
		stream.SetStartTime(-0.01)
	})
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

func TestDeepgramSTTv2StreamResamplesInputAudioToReferenceRate(t *testing.T) {
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
		WithDeepgramSTTv2SampleRate(16000),
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(480),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	select {
	case got := <-audioFrames:
		t.Fatalf("audio frame before Flush = %#v, want resampled frame buffered below stream chunk size", got)
	default:
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case got := <-audioFrames:
		want := deepgramEveryNthInt16PCM(480, 3)
		if !bytes.Equal(got, want) {
			t.Fatalf("flushed audio frame = %#v, want 48k->16k reference resampled PCM", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for resampled audio frame")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTv2StreamResamplesInputAudioWithReferenceTiming(t *testing.T) {
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
		WithDeepgramSTTv2SampleRate(16000),
	)
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	frame := deepgramTestInt16PCM(1)
	for i := 0; i < 2204; i++ {
		if err := stream.PushFrame(&model.AudioFrame{
			Data:              frame,
			SampleRate:        44100,
			NumChannels:       1,
			SamplesPerChannel: 1,
		}); err != nil {
			t.Fatalf("PushFrame frame %d error = %v", i, err)
		}
	}
	select {
	case got := <-audioFrames:
		t.Fatalf("audio frame before 50ms source duration = %#v, want none", got)
	default:
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              frame,
		SampleRate:        44100,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}); err != nil {
		t.Fatalf("PushFrame frame 2205 error = %v", err)
	}
	select {
	case got := <-audioFrames:
		if len(got) != 1600 {
			t.Fatalf("audio frame length = %d, want 50ms 16k mono PCM", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for 50ms resampled audio frame")
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("provider Close() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTv2StreamRejectsReferenceSampleRateChange(t *testing.T) {
	stream := &deepgramV2Stream{sampleRate: 16000}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(160),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	}); err != nil {
		t.Fatalf("first PushFrame() error = %v", err)
	}
	err := stream.PushFrame(&model.AudioFrame{
		Data:              deepgramTestInt16PCM(160),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 160,
	})
	if err == nil || err.Error() != "the sample rate of the input frames must be consistent" {
		t.Fatalf("second PushFrame() error = %v, want reference sample-rate consistency error", err)
	}
}

func TestDeepgramSTTv2StreamUsesReferenceDefaultLanguage(t *testing.T) {
	closeSeen := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramSTTv2DefaultLanguageWebsocketServer(serverConn, closeSeen, serverErr)

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
	)
	stream, err := provider.Stream(context.Background(), "fr")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

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
	if interim.Type != stt.SpeechEventInterimTranscript {
		t.Fatalf("event type = %s, want interim transcript", interim.Type)
	}
	if got := interim.Alternatives[0].Language; got != "en" {
		t.Fatalf("transcript language = %q, want reference default en", got)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTv2EndInputFlushesTailAndClosesReferenceInput(t *testing.T) {
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
		WithDeepgramSTTv2SampleRate(16000),
	)
	rawStream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := rawStream.(*deepgramV2Stream)
	defer stream.Close()

	if _, ok := rawStream.(stt.InputEnding); !ok {
		t.Fatalf("stream = %T, want stt.InputEnding", rawStream)
	}
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 2400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1200,
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
	if err := stream.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	select {
	case got := <-audioFrames:
		if len(got) != 800 {
			t.Fatalf("end input audio frame length = %d, want 800", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for end input audio tail")
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{1}}); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("PushFrame after EndInput error = %v, want stream input ended", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "stream input ended") {
		t.Fatalf("Flush after EndInput error = %v, want stream input ended", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTv2EndInputTreatsProviderCloseAsExpected(t *testing.T) {
	closeSeen := make(chan struct{})
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runDeepgramSTTv2CloseAfterCloseStreamServer(serverConn, closeSeen, serverErr)

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

	provider := NewDeepgramSTTv2("test-key", WithDeepgramSTTv2BaseURL("ws://deepgram.test/v2/listen"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	ending, ok := stream.(stt.InputEnding)
	if !ok {
		t.Fatalf("stream = %T, want stt.InputEnding", stream)
	}
	if err := ending.EndInput(); err != nil {
		t.Fatalf("EndInput() error = %v", err)
	}
	select {
	case <-closeSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for CloseStream")
	}

	_, err = stream.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() after provider close error = %T %v, want EOF", err, err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
	}
}

func TestDeepgramSTTv2StreamSendsReferenceHeartbeatPing(t *testing.T) {
	oldInterval := deepgramSTTv2HeartbeatInterval
	deepgramSTTv2HeartbeatInterval = 10 * time.Millisecond
	defer func() {
		deepgramSTTv2HeartbeatInterval = oldInterval
	}()

	pingSeen := make(chan struct{})
	clientConn, serverConn := net.Pipe()
	serverErr := make(chan error, 1)
	go runDeepgramSTTv2HeartbeatWebsocketServer(serverConn, pingSeen, serverErr)

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

	provider := NewDeepgramSTTv2("test-key", WithDeepgramSTTv2BaseURL("ws://deepgram.test/v2/listen"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	select {
	case <-pingSeen:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for STTv2 heartbeat ping")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("test websocket server error: %v", err)
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

func TestDeepgramSTTv2RejectsReferenceInvalidTurnOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []DeepgramSTTv2Option
		want string
	}{
		{
			name: "eager end of turn above default end of turn",
			opts: []DeepgramSTTv2Option{WithDeepgramSTTv2EagerEOTThreshold(0.9)},
			want: "eager_eot_threshold (0.9) must be less than or equal to eot_threshold (0.7)",
		},
		{
			name: "eager end of turn above configured end of turn",
			opts: []DeepgramSTTv2Option{
				WithDeepgramSTTv2EagerEOTThreshold(0.8),
				WithDeepgramSTTv2EOTThreshold(0.6),
			},
			want: "eager_eot_threshold (0.8) must be less than or equal to eot_threshold (0.6)",
		},
		{
			name: "long tag",
			opts: []DeepgramSTTv2Option{WithDeepgramSTTv2Tags([]string{strings.Repeat("x", 129)})},
			want: "tag must be no more than 128 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewDeepgramSTTv2("test-key", tt.opts...)
			_, err := provider.Stream(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Stream() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDeepgramSTTv2UpdateOptionsMatchesReferenceFutureStreams(t *testing.T) {
	provider := NewDeepgramSTTv2("test-key",
		WithDeepgramSTTv2BaseURL("https://deepgram.example/v2/listen"),
		WithDeepgramSTTv2EOTThreshold(0.7),
		WithDeepgramSTTv2Tags([]string{"initial"}),
	)

	if err := provider.UpdateOptions(
		WithDeepgramSTTv2BaseURL("https://updated.deepgram.example/v2/listen"),
		WithDeepgramSTTv2Model("flux-general-multi"),
		WithDeepgramSTTv2SampleRate(48000),
		WithDeepgramSTTv2MipOptOut(true),
		WithDeepgramSTTv2EagerEOTThreshold(0.6),
		WithDeepgramSTTv2EOTThreshold(0.8),
		WithDeepgramSTTv2EOTTimeout(1500),
		WithDeepgramSTTv2Keyterms([]string{"LiveKit"}),
		WithDeepgramSTTv2Tags([]string{"agent"}),
		WithDeepgramSTTv2LanguageHints([]string{"en", "es"}),
	); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	parsed, err := url.Parse(buildDeepgramSTTv2StreamURL(provider))
	if err != nil {
		t.Fatalf("parse STTv2 URL: %v", err)
	}
	if parsed.Host != "updated.deepgram.example" || parsed.Scheme != "wss" {
		t.Fatalf("stream URL = %s, want updated wss endpoint", parsed.String())
	}
	query := parsed.Query()
	assertDeepgramQuery(t, query, "model", "flux-general-multi")
	assertDeepgramQuery(t, query, "sample_rate", "48000")
	assertDeepgramQuery(t, query, "mip_opt_out", "true")
	assertDeepgramQuery(t, query, "eager_eot_threshold", "0.6")
	assertDeepgramQuery(t, query, "eot_threshold", "0.8")
	assertDeepgramQuery(t, query, "eot_timeout_ms", "1500")
	assertDeepgramQueryValues(t, query, "keyterm", []string{"LiveKit"})
	assertDeepgramQueryValues(t, query, "tag", []string{"agent"})
	assertDeepgramQueryValues(t, query, "language_hint", []string{"en", "es"})
}

func TestDeepgramSTTv2UpdateOptionsRejectsInvalidWithoutMutation(t *testing.T) {
	provider := NewDeepgramSTTv2("test-key", WithDeepgramSTTv2EOTThreshold(0.8))
	before := buildDeepgramSTTv2StreamURL(provider)

	err := provider.UpdateOptions(WithDeepgramSTTv2EagerEOTThreshold(0.9))
	if err == nil || !strings.Contains(err.Error(), "eager_eot_threshold (0.9) must be less than or equal to eot_threshold (0.8)") {
		t.Fatalf("UpdateOptions() error = %v, want invalid eager threshold", err)
	}
	if after := buildDeepgramSTTv2StreamURL(provider); after != before {
		t.Fatalf("stream URL after failed update = %s, want unchanged %s", after, before)
	}
}

func TestDeepgramSTTv2UpdateOptionsReconnectsActiveStream(t *testing.T) {
	requests := make(chan *url.URL, 2)
	audioMessages := make(chan []byte, 1)
	serverErr := make(chan error, 2)

	oldDialer := websocket.DefaultDialer
	websocket.DefaultDialer = &websocket.Dialer{
		NetDialContext: func(context.Context, string, string) (net.Conn, error) {
			clientConn, serverConn := net.Pipe()
			go runDeepgramReconnectRecordingWebsocketServer(serverConn, requests, audioMessages, serverErr)
			return clientConn, nil
		},
		Proxy: nil,
	}
	defer func() {
		websocket.DefaultDialer = oldDialer
	}()

	provider := NewDeepgramSTTv2("test-key", WithDeepgramSTTv2BaseURL("ws://deepgram.test/v2/listen"))
	stream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer stream.Close()

	firstURL := receiveDeepgramTestRequestURL(t, requests, "first STTv2 websocket request")
	assertDeepgramQuery(t, firstURL.Query(), "model", "flux-general-en")
	assertDeepgramQuery(t, firstURL.Query(), "sample_rate", "16000")

	if err := provider.UpdateOptions(
		WithDeepgramSTTv2Model("flux-general-multi"),
		WithDeepgramSTTv2SampleRate(48000),
		WithDeepgramSTTv2EagerEOTThreshold(0.6),
		WithDeepgramSTTv2EOTThreshold(0.8),
	); err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}

	secondURL := receiveDeepgramTestRequestURL(t, requests, "updated STTv2 websocket request")
	assertDeepgramQuery(t, secondURL.Query(), "model", "flux-general-multi")
	assertDeepgramQuery(t, secondURL.Query(), "sample_rate", "48000")
	assertDeepgramQuery(t, secondURL.Query(), "eager_eot_threshold", "0.6")
	assertDeepgramQuery(t, secondURL.Query(), "eot_threshold", "0.8")

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 4800),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 2400,
	}); err != nil {
		t.Fatalf("PushFrame after update error = %v", err)
	}
	select {
	case got := <-audioMessages:
		if len(got) == 0 {
			t.Fatal("updated stream audio is empty")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio on updated STTv2 websocket")
	}
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "closed pipe") {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTv2UpdateOptionsDoesNotHoldProviderLockWhileUpdatingStream(t *testing.T) {
	provider := NewDeepgramSTTv2("test-key")
	stream := &deepgramV2Stream{provider: provider, streamURL: buildDeepgramSTTv2StreamURL(provider)}
	provider.streams[stream] = struct{}{}

	stream.mu.Lock()
	updateDone := make(chan error, 1)
	go func() {
		updateDone <- provider.UpdateOptions(WithDeepgramSTTv2Model("flux-general-multi"))
	}()

	deadline := time.After(time.Second)
	for {
		select {
		case err := <-updateDone:
			stream.mu.Unlock()
			t.Fatalf("UpdateOptions returned before stream lock released: %v", err)
		case <-deadline:
			stream.mu.Unlock()
			t.Fatal("provider lock stayed held while UpdateOptions waited for stream lock")
		default:
		}

		if provider.mu.TryLock() {
			updated := provider.model == "flux-general-multi"
			provider.mu.Unlock()
			if updated {
				break
			}
		}
		time.Sleep(time.Millisecond)
	}

	stream.mu.Unlock()
	if err := <-updateDone; err != nil {
		t.Fatalf("UpdateOptions() error = %v", err)
	}
}

func TestDeepgramSTTv2StreamEmitsReferenceRecognitionUsage(t *testing.T) {
	requests := make(chan *url.URL, 1)
	audioMessages := make(chan []byte, 2)
	serverErr := make(chan error, 1)
	clientConn, serverConn := net.Pipe()
	go runDeepgramReconnectRecordingWebsocketServer(serverConn, requests, audioMessages, serverErr)

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

	provider := NewDeepgramSTTv2("test-key", WithDeepgramSTTv2BaseURL("ws://deepgram.test/v2/listen"))
	rawStream, err := provider.Stream(context.Background(), "en")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	stream := rawStream.(*deepgramV2Stream)
	stream.requestID = "req-usage"
	defer stream.Close()

	_ = receiveDeepgramTestRequestURL(t, requests, "usage STTv2 websocket request")
	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 2000),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 1000,
	}); err != nil {
		t.Fatalf("PushFrame() error = %v", err)
	}
	select {
	case <-audioMessages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for STTv2 audio frame")
	}
	assertNoDeepgramRecognitionUsageEvent(t, stream.events)

	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	select {
	case <-audioMessages:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for flushed STTv2 audio frame")
	}
	select {
	case event := <-stream.events:
		if event.Type != stt.SpeechEventRecognitionUsage {
			t.Fatalf("event type = %s, want %s", event.Type, stt.SpeechEventRecognitionUsage)
		}
		if event.RequestID != "req-usage" {
			t.Fatalf("usage request id = %q, want req-usage", event.RequestID)
		}
		if event.RecognitionUsage == nil {
			t.Fatal("RecognitionUsage = nil")
		}
		if event.RecognitionUsage.AudioDuration != 0.0625 {
			t.Fatalf("AudioDuration = %v, want 0.0625", event.RecognitionUsage.AudioDuration)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recognition usage event")
	}
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) && !strings.Contains(err.Error(), "closed pipe") {
			t.Fatalf("test websocket server error: %v", err)
		}
	default:
	}
}

func TestDeepgramSTTv2NextAfterCloseDrainsQueuedEvent(t *testing.T) {
	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "queued final",
		}},
	}

	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stream := &deepgramV2Stream{
			ctx:    ctx,
			events: make(chan *stt.SpeechEvent, 1),
			closed: true,
		}
		stream.events <- want

		got, err := stream.Next()
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v, want queued event", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: Next() event = %+v, want queued final transcript %+v", i, got, want)
		}
	}
}

func TestDeepgramSTTv2NextReturnsQueuedTranscriptBeforeStreamError(t *testing.T) {
	want := &stt.SpeechEvent{
		Type: stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{
			Text: "queued final",
		}},
	}

	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		stream := &deepgramV2Stream{
			ctx:    ctx,
			events: make(chan *stt.SpeechEvent, 1),
			errCh:  make(chan error, 1),
		}
		stream.events <- want
		stream.errCh <- errors.New("stream failed")

		got, err := stream.Next()
		cancel()
		if err != nil {
			t.Fatalf("iteration %d: Next() error = %v, want queued event before stream error", i, err)
		}
		if got != want {
			t.Fatalf("iteration %d: Next() event = %+v, want queued final transcript %+v", i, got, want)
		}
	}
}

func TestDeepgramSTTv2SendEventWaitsForConsumer(t *testing.T) {
	stream := &deepgramV2Stream{
		ctx:    context.Background(),
		events: make(chan *stt.SpeechEvent, 1),
	}
	first := &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript}
	second := &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript}
	stream.events <- first

	sent := make(chan struct{})
	go func() {
		stream.sendEvent(second)
		close(sent)
	}()

	select {
	case <-sent:
		t.Fatal("sendEvent returned while event queue was full; want reference non-lossy delivery")
	case <-time.After(100 * time.Millisecond):
	}

	if got := <-stream.events; got != first {
		t.Fatalf("first drained event = %+v, want %+v", got, first)
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("sendEvent did not unblock after consumer drained queue")
	}
	if got := <-stream.events; got != second {
		t.Fatalf("second drained event = %+v, want %+v", got, second)
	}
}

func TestDeepgramSTTv2CloseUnblocksBackpressuredEventSend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &deepgramV2Stream{
		ctx:    ctx,
		cancel: cancel,
		events: make(chan *stt.SpeechEvent, 1),
		errCh:  make(chan error, 1),
	}
	stream.events <- &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript}

	sendStarted := make(chan struct{})
	sendDone := make(chan struct{})
	go func() {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		close(sendStarted)
		stream.sendEvent(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript})
		close(sendDone)
	}()

	select {
	case <-sendStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocked event send")
	}
	select {
	case <-sendDone:
		t.Fatal("sendEvent returned before Close canceled stream context")
	case <-time.After(100 * time.Millisecond):
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock backpressured event send")
	}
	select {
	case <-sendDone:
	case <-time.After(time.Second):
		t.Fatal("blocked sendEvent did not exit after Close")
	}
}

func TestDeepgramSTTv2UsageFlushDoesNotBlockConsumer(t *testing.T) {
	stream := &deepgramV2Stream{
		ctx:        context.Background(),
		events:     make(chan *stt.SpeechEvent, 1),
		errCh:      make(chan error, 1),
		requestID:  "req-usage",
		usageTotal: 0.25,
	}
	first := &stt.SpeechEvent{Type: stt.SpeechEventInterimTranscript}
	stream.events <- first

	flushDone := make(chan struct{})
	go func() {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		stream.flushRecognitionUsageLocked()
		close(flushDone)
	}()

	nextDone := make(chan *stt.SpeechEvent, 1)
	nextErr := make(chan error, 1)
	go func() {
		event, err := stream.Next()
		nextDone <- event
		nextErr <- err
	}()

	select {
	case event := <-nextDone:
		if err := <-nextErr; err != nil {
			t.Fatalf("Next() error = %v, want queued event", err)
		}
		if event != first {
			t.Fatalf("Next() event = %+v, want first queued event %+v", event, first)
		}
	case <-time.After(200 * time.Millisecond):
		<-stream.events
		<-flushDone
		t.Fatal("Next() blocked behind usage flush stream lock")
	}

	select {
	case <-flushDone:
	case <-time.After(time.Second):
		t.Fatal("usage flush did not finish after consumer drained queue")
	}
	usage, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() usage error = %v", err)
	}
	if usage.Type != stt.SpeechEventRecognitionUsage || usage.RequestID != "req-usage" {
		t.Fatalf("usage event = %+v, want recognition_usage for req-usage", usage)
	}
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

func runDeepgramSTTv2DefaultLanguageWebsocketServer(conn net.Conn, closeSeen chan<- struct{}, errCh chan<- error) {
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
	msg := `{"type":"TurnInfo","event":"StartOfTurn","request_id":"req-v2","transcript":"hello","audio_window_start":0.1,"audio_window_end":0.4,"words":[{"word":"hello","start":0.1,"end":0.4,"confidence":0.9}]}`
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
		if opcode == websocket.TextMessage && deepgramTestWebsocketMessageType(payload) == "CloseStream" {
			close(closeSeen)
			errCh <- nil
			return
		}
	}
}

func runDeepgramSTTv2CloseAfterCloseStreamServer(conn net.Conn, closeSeen chan<- struct{}, errCh chan<- error) {
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

	for {
		opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		if opcode != websocket.TextMessage || deepgramTestWebsocketMessageType(payload) != "CloseStream" {
			continue
		}
		close(closeSeen)
		closePayload := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done")
		if err := writeDeepgramTestWebsocketFrame(conn, websocket.CloseMessage, closePayload); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
		return
	}
}

func runDeepgramSTTv2HeartbeatWebsocketServer(conn net.Conn, pingSeen chan<- struct{}, errCh chan<- error) {
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

	for {
		opcode, payload, err := readDeepgramSTTTestClientWebsocketFrame(reader)
		if err != nil {
			errCh <- err
			return
		}
		switch opcode {
		case websocket.PingMessage:
			close(pingSeen)
			errCh <- nil
			return
		case websocket.TextMessage:
			if deepgramTestWebsocketMessageType(payload) == "CloseStream" {
				continue
			}
		}
	}
}
