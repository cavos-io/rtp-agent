package telnyx

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestTelnyxSTTDefaultsMatchReference(t *testing.T) {
	provider := NewTelnyxSTT("test-key")

	if provider.baseURL != "wss://api.telnyx.com/v2/speech-to-text/transcription" {
		t.Fatalf("base URL = %q, want reference websocket endpoint", provider.baseURL)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.transcriptionEngine != "telnyx" {
		t.Fatalf("engine = %q, want telnyx", provider.transcriptionEngine)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if got := provider.InputSampleRate(); got != 16000 {
		t.Fatalf("InputSampleRate() = %d, want reference sample rate 16000", got)
	}
	if got := stt.Model(provider); got != "telnyx" {
		t.Fatalf("model metadata = %q, want telnyx", got)
	}
	if got := stt.Provider(provider); got != "telnyx" {
		t.Fatalf("provider metadata = %q, want telnyx", got)
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming interim offline recognize", caps)
	}
}

func TestTelnyxSTTExposesConfiguredInputSampleRate(t *testing.T) {
	provider := NewTelnyxSTT("test-key", WithTelnyxSTTSampleRate(8000))

	if got := provider.InputSampleRate(); got != 8000 {
		t.Fatalf("InputSampleRate() = %d, want configured sample rate 8000", got)
	}
}

func TestTelnyxSTTInterimResultsOptionMatchesReference(t *testing.T) {
	provider := NewTelnyxSTT("test-key", WithTelnyxSTTInterimResults(false))

	caps := provider.Capabilities()
	if !caps.Streaming || caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming without interim and with offline recognize", caps)
	}
}

func TestNewTelnyxSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "env-key")

	provider := NewTelnyxSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewTelnyxSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestTelnyxSTTStreamRequiresAPIKeyBeforeDial(t *testing.T) {
	t.Setenv("TELNYX_API_KEY", "")
	provider := NewTelnyxSTT("", WithTelnyxSTTBaseURL("://bad-url"))

	_, err := provider.Stream(context.Background(), "")

	if err == nil || !strings.Contains(err.Error(), "TELNYX_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", err)
	}
}

func TestTelnyxSTTStreamURLAndHeadersMatchReference(t *testing.T) {
	provider := NewTelnyxSTT("test-key",
		WithTelnyxSTTBaseURL("wss://telnyx.example/transcription"),
		WithTelnyxSTTLanguage("es"),
		WithTelnyxSTTTranscriptionEngine("deepgram"),
	)

	streamURL, err := url.Parse(buildTelnyxSTTStreamURL(provider, ""))
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	query := streamURL.Query()
	if streamURL.String()[:len("wss://telnyx.example/transcription?")] != "wss://telnyx.example/transcription?" {
		t.Fatalf("stream URL = %q, want configured websocket URL", streamURL.String())
	}
	if query.Get("transcription_engine") != "deepgram" || query.Get("language") != "es" || query.Get("input_format") != "wav" {
		t.Fatalf("query = %+v, want engine language wav", query)
	}
	if buildTelnyxSTTHeaders(provider).Get("Authorization") != "Bearer test-key" {
		t.Fatal("Authorization header missing bearer token")
	}

	overrideURL, _ := url.Parse(buildTelnyxSTTStreamURL(provider, "fr"))
	if overrideURL.Query().Get("language") != "fr" {
		t.Fatalf("override language = %q, want fr", overrideURL.Query().Get("language"))
	}
}

func TestTelnyxSTTWAVHeaderMatchesReference(t *testing.T) {
	header := createTelnyxStreamingWAVHeader(16000, 1)

	if len(header) != 44 {
		t.Fatalf("header length = %d, want 44", len(header))
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" || string(header[36:40]) != "data" {
		t.Fatalf("header identifiers invalid: %q %q %q", header[0:4], header[8:12], header[36:40])
	}
	if sampleRate := binary.LittleEndian.Uint32(header[24:28]); sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", sampleRate)
	}
	if dataSize := binary.LittleEndian.Uint32(header[40:44]); dataSize != 0x7fffffff {
		t.Fatalf("data size = %x, want streaming max", dataSize)
	}
}

func TestTelnyxSTTStreamChunksAndFlushesReferenceAudio(t *testing.T) {
	var writes [][]byte
	stream := &telnyxSTTStream{
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	}); err != nil {
		t.Fatalf("PushFrame half chunk error = %v", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes = %d, want no write before 50ms chunk", len(writes))
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	}); err != nil {
		t.Fatalf("PushFrame full chunk error = %v", err)
	}
	if len(writes) != 1 || len(writes[0]) != 1600 {
		t.Fatalf("writes = %s, want one 50ms 1600-byte chunk", telnyxWriteSizes(writes))
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 400),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 200,
	}); err != nil {
		t.Fatalf("PushFrame remainder error = %v", err)
	}
	if err := stream.Flush(); err != nil {
		t.Fatalf("Flush error = %v", err)
	}
	if len(writes) != 2 || len(writes[1]) != 400 {
		t.Fatalf("writes after flush = %s, want flushed 400-byte remainder", telnyxWriteSizes(writes))
	}
}

func TestTelnyxSTTStreamClosesAfterAudioWriteFailure(t *testing.T) {
	writeErr := errors.New("write failed")
	cancelled := false
	closeCalls := 0
	stream := &telnyxSTTStream{
		cancel: func() { cancelled = true },
		writeBinary: func([]byte) error {
			return writeErr
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	})
	if !errors.Is(err, writeErr) {
		t.Fatalf("PushFrame error = %v, want write error", err)
	}
	if !cancelled {
		t.Fatal("cancel not called after write failure")
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	err = stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 1600),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 800,
	})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushFrame after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Flush(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Flush after write failure error = %v, want closed stream error", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close after write failure error = %v, want nil", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after idempotent Close = %d, want 1", closeCalls)
	}
}

func TestTelnyxSTTProviderCloseClosesActiveStreams(t *testing.T) {
	provider := NewTelnyxSTT("test-key")
	closeCalls := 0
	stream := &telnyxSTTStream{
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}
	provider.registerStream(stream)

	if err := provider.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
	if err := stream.PushFrame(&model.AudioFrame{Data: []byte{0x01, 0x02}}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("PushFrame after provider Close error = %v, want closed error", err)
	}
}

func TestTelnyxSTTStreamCloseFlushesBufferedAudioBeforeClose(t *testing.T) {
	var writes [][]byte
	closeCalls := 0
	stream := &telnyxSTTStream{
		cancel: func() {},
		writeBinary: func(data []byte) error {
			writes = append(writes, append([]byte(nil), data...))
			return nil
		},
		closeConn: func() error {
			closeCalls++
			return nil
		},
	}

	if err := stream.PushFrame(&model.AudioFrame{
		Data:              make([]byte, 800),
		SampleRate:        16000,
		NumChannels:       1,
		SamplesPerChannel: 400,
	}); err != nil {
		t.Fatalf("PushFrame error = %v, want nil", err)
	}
	if len(writes) != 0 {
		t.Fatalf("writes before close = %s, want none", telnyxWriteSizes(writes))
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	if len(writes) != 1 || len(writes[0]) != 800 {
		t.Fatalf("writes after close = %s, want buffered 800-byte audio", telnyxWriteSizes(writes))
	}
	if closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", closeCalls)
	}
}

func TestTelnyxSTTFinalTranscriptCollectsAllReferenceFinals(t *testing.T) {
	stream := &fakeTelnyxRecognizeStream{events: []*stt.SpeechEvent{
		{Type: stt.SpeechEventInterimTranscript, Alternatives: []stt.SpeechData{{Text: "ignored"}}},
		{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "hello "}}},
		{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "world"}}},
	}}

	event, err := collectTelnyxFinalTranscript(stream, "en")
	if err != nil {
		t.Fatalf("collect final transcript error = %v, want nil", err)
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 || event.Alternatives[0].Text != "hello world" {
		t.Fatalf("alternatives = %+v, want concatenated final text", event.Alternatives)
	}
}

func TestTelnyxSTTEventsMatchReferenceLifecycle(t *testing.T) {
	state := &telnyxSTTStreamState{language: "en"}

	events, err := processTelnyxSTTEvent(state, map[string]any{
		"transcript": "hello",
		"is_final":   false,
		"confidence": 0.7,
	})
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertTelnyxSTTEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertTelnyxSTTEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")

	events, err = processTelnyxSTTEvent(state, map[string]any{
		"transcript": "hello final",
		"is_final":   true,
		"confidence": 0.9,
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertTelnyxSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello final")
	assertTelnyxSTTEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func assertTelnyxSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event type = %v, want %v", events[index].Type, eventType)
	}
	if text == "" {
		return
	}
	if len(events[index].Alternatives) != 1 || events[index].Alternatives[0].Text != text {
		t.Fatalf("alternatives = %+v, want text %q", events[index].Alternatives, text)
	}
}

func telnyxWriteSizes(writes [][]byte) string {
	sizes := make([]string, 0, len(writes))
	for _, write := range writes {
		sizes = append(sizes, strconv.Itoa(len(write)))
	}
	return strings.Join(sizes, ",")
}

type fakeTelnyxRecognizeStream struct {
	events []*stt.SpeechEvent
	index  int
}

func (f *fakeTelnyxRecognizeStream) PushFrame(*model.AudioFrame) error { return nil }
func (f *fakeTelnyxRecognizeStream) Flush() error                      { return nil }
func (f *fakeTelnyxRecognizeStream) Close() error                      { return nil }

func (f *fakeTelnyxRecognizeStream) Next() (*stt.SpeechEvent, error) {
	if f.index >= len(f.events) {
		return nil, io.EOF
	}
	event := f.events[f.index]
	f.index++
	return event, nil
}
