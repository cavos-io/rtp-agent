package baseten

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestBasetenSTTDefaultsMatchReferenceOptions(t *testing.T) {
	provider := NewBasetenSTT("test-key", "model-id")

	if provider.modelEndpoint != "wss://model-model-id.api.baseten.co/environments/production/websocket" {
		t.Fatalf("endpoint = %q, want generated truss websocket endpoint", provider.modelEndpoint)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.bufferSizeSeconds != 0.032 {
		t.Fatalf("buffer size = %.3f, want 0.032", provider.bufferSizeSeconds)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if !provider.enablePartialTranscripts || !provider.showWordTimestamps {
		t.Fatalf("partial=%v word timestamps=%v, want both true", provider.enablePartialTranscripts, provider.showWordTimestamps)
	}

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.OfflineRecognize || caps.AlignedTranscript != "word" {
		t.Fatalf("capabilities = %+v, want streaming interim word-aligned only", caps)
	}
}

func TestBasetenSTTEndpointOptionsMatchReferencePriority(t *testing.T) {
	explicit := NewBasetenSTT("test-key", "ignored",
		WithBasetenSTTModelEndpoint("wss://explicit.example/websocket"),
		WithBasetenSTTChainID("chain-1"),
	)
	if explicit.modelEndpoint != "wss://explicit.example/websocket" {
		t.Fatalf("explicit endpoint = %q, want highest priority endpoint", explicit.modelEndpoint)
	}

	chain := NewBasetenSTT("test-key", "",
		WithBasetenSTTChainID("chain-1"),
	)
	if chain.modelEndpoint != "wss://chain-chain-1.api.baseten.co/environments/production/websocket" {
		t.Fatalf("chain endpoint = %q, want generated chain endpoint", chain.modelEndpoint)
	}
}

func TestBuildBasetenSTTMetadataMatchesReferenceSchema(t *testing.T) {
	provider := NewBasetenSTT("test-key", "model-id",
		WithBasetenSTTLanguage("auto"),
		WithBasetenSTTEncoding("pcm_mulaw"),
		WithBasetenSTTVADThreshold(0.7),
	)

	metadata := buildBasetenSTTMetadata(provider)
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	whisper := decoded["whisper_params"].(map[string]any)
	if whisper["audio_language"] != "auto" || whisper["show_word_timestamps"] != true {
		t.Fatalf("whisper params = %+v, want language and timestamps", whisper)
	}
	streaming := decoded["streaming_params"].(map[string]any)
	if streaming["encoding"] != "pcm_mulaw" || streaming["sample_rate"] != float64(16000) {
		t.Fatalf("streaming params = %+v, want encoding and sample rate", streaming)
	}
	if streaming["enable_partial_transcripts"] != true || streaming["final_transcript_max_duration_s"] != float64(30) {
		t.Fatalf("streaming params = %+v, want partial and final duration defaults", streaming)
	}
	vad := decoded["streaming_vad_config"].(map[string]any)
	if vad["threshold"] != float64(0.7) || vad["min_silence_duration_ms"] != float64(300) || vad["speech_pad_ms"] != float64(30) {
		t.Fatalf("vad config = %+v, want reference vad values", vad)
	}
}

func TestBasetenSTTTranscriptEventsMapReferenceMessages(t *testing.T) {
	state := &basetenSTTStreamState{language: "en", startTimeOffset: 1.5}
	events, err := processBasetenSTTMessage(state, []byte(`{
		"type":"transcription",
		"is_final":false,
		"transcript":"hello",
		"confidence":0.75,
		"segments":[{"text":"hello","start_time":0.1,"end_time":0.4,
			"word_timestamps":[{"word":"hello","start_time":0.1,"end_time":0.4}]}]
	}`))
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertBasetenSTTEvent(t, events, 0, stt.SpeechEventInterimTranscript, "hello")
	if events[0].Alternatives[0].Words[0].StartTime != 1.6 {
		t.Fatalf("word start = %.1f, want offset applied", events[0].Alternatives[0].Words[0].StartTime)
	}

	events, err = processBasetenSTTMessage(state, []byte(`{
		"type":"transcription",
		"is_final":true,
		"language_code":"es",
		"segments":[{"text":"hola","start_time":0.2,"end_time":0.5}]
	}`))
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertBasetenSTTEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hola")
	if events[0].Alternatives[0].Language != "es" {
		t.Fatalf("language = %q, want es", events[0].Alternatives[0].Language)
	}
}

func TestBasetenSTTRecognizeIsUnsupportedLikeReference(t *testing.T) {
	_, err := NewBasetenSTT("test-key", "model-id").Recognize(context.Background(), nil, "")
	if err == nil || !strings.Contains(err.Error(), "does not support offline recognize") {
		t.Fatalf("error = %v, want offline recognize unsupported", err)
	}
}

func assertBasetenSTTEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
	t.Helper()
	if len(events) <= index {
		t.Fatalf("events length = %d, missing index %d", len(events), index)
	}
	if events[index].Type != eventType {
		t.Fatalf("event type = %v, want %v", events[index].Type, eventType)
	}
	if len(events[index].Alternatives) != 1 || events[index].Alternatives[0].Text != text {
		t.Fatalf("alternatives = %+v, want text %q", events[index].Alternatives, text)
	}
}
