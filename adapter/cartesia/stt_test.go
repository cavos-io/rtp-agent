package cartesia

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	audiomodel "github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
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
