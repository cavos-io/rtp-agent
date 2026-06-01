package soniox

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

func TestSonioxSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSonioxSTT("test-key")

	if provider.baseURL != "wss://stt-rt.soniox.com/transcribe-websocket" {
		t.Fatalf("base URL = %q, want reference websocket URL", provider.baseURL)
	}
	if provider.model != "stt-rt-v4" {
		t.Fatalf("model = %q, want stt-rt-v4", provider.model)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.numChannels != 1 {
		t.Fatalf("num channels = %d, want 1", provider.numChannels)
	}
	if provider.maxEndpointDelayMS != 500 {
		t.Fatalf("max endpoint delay = %d, want 500", provider.maxEndpointDelayMS)
	}
	if !provider.enableLanguageIdentification {
		t.Fatal("language identification = false, want true")
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.AlignedTranscript != "chunk" {
		t.Fatalf("aligned transcript = %q, want chunk", caps.AlignedTranscript)
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestSonioxSTTOptionsBuildReferenceConfig(t *testing.T) {
	provider := NewSonioxSTT("test-key",
		WithSonioxBaseURL("ws://soniox.example/ws"),
		WithSonioxModel("stt-rt-v3-preview"),
		WithSonioxLanguageHints([]string{"en", "es"}),
		WithSonioxLanguageHintsStrict(true),
		WithSonioxContextText("domain words"),
		WithSonioxNumChannels(2),
		WithSonioxSampleRate(8000),
		WithSonioxSpeakerDiarization(true),
		WithSonioxLanguageIdentification(false),
		WithSonioxMaxEndpointDelayMS(1200),
		WithSonioxClientReferenceID("client-1"),
		WithSonioxOneWayTranslation("fr"),
	)

	config := buildSonioxConfig(provider)

	assertSonioxConfig(t, config, "api_key", "test-key")
	assertSonioxConfig(t, config, "model", "stt-rt-v3-preview")
	assertSonioxConfig(t, config, "audio_format", "pcm_s16le")
	if config["num_channels"] != 2 {
		t.Fatalf("num_channels = %#v, want 2", config["num_channels"])
	}
	if config["sample_rate"] != 8000 {
		t.Fatalf("sample_rate = %#v, want 8000", config["sample_rate"])
	}
	if config["language_hints_strict"] != true {
		t.Fatalf("language_hints_strict = %#v, want true", config["language_hints_strict"])
	}
	if config["context"] != "domain words" {
		t.Fatalf("context = %#v, want context text", config["context"])
	}
	if config["enable_speaker_diarization"] != true {
		t.Fatalf("enable_speaker_diarization = %#v, want true", config["enable_speaker_diarization"])
	}
	if config["enable_language_identification"] != false {
		t.Fatalf("enable_language_identification = %#v, want false", config["enable_language_identification"])
	}
	if config["max_endpoint_delay_ms"] != 1200 {
		t.Fatalf("max_endpoint_delay_ms = %#v, want 1200", config["max_endpoint_delay_ms"])
	}
	if config["client_reference_id"] != "client-1" {
		t.Fatalf("client_reference_id = %#v, want client-1", config["client_reference_id"])
	}
	hints := config["language_hints"].([]string)
	if len(hints) != 2 || hints[0] != "en" || hints[1] != "es" {
		t.Fatalf("language_hints = %#v, want en/es", hints)
	}
	translation := config["translation"].(map[string]string)
	if translation["type"] != "one_way" || translation["target_language"] != "fr" {
		t.Fatalf("translation = %#v, want one-way French", translation)
	}
}

func TestSonioxSTTInitialConfigJSONOmitsNilOptionalFields(t *testing.T) {
	provider := NewSonioxSTT("test-key")

	payload, err := buildSonioxConfigJSON(provider)
	if err != nil {
		t.Fatalf("build config json: %v", err)
	}
	if strings.Contains(string(payload), "client_reference_id") {
		t.Fatalf("payload includes nil client_reference_id: %s", payload)
	}
	if !strings.Contains(string(payload), `"api_key":"test-key"`) {
		t.Fatalf("payload = %s, want api key", payload)
	}
}

func TestSonioxSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewSonioxSTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "does not support single frame recognition") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestSonioxTokenAccumulatorBuildsSpeechData(t *testing.T) {
	acc := &sonioxTokenAccumulator{}
	acc.update(sonioxToken{Text: "hello ", Language: "en", Speaker: anyFloat64(2), StartMS: anyFloat64(100), EndMS: anyFloat64(250), Confidence: anyFloat64(0.8)})
	acc.update(sonioxToken{Text: "world", Language: "en", EndMS: anyFloat64(500), Confidence: anyFloat64(1.0)})

	data := acc.toSpeechData(1.5, nil, nil, nil, nil)

	if data.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", data.Text)
	}
	if data.Language != "en" {
		t.Fatalf("language = %q, want en", data.Language)
	}
	if data.SpeakerID != "2" {
		t.Fatalf("speaker = %q, want 2", data.SpeakerID)
	}
	if data.StartTime != 1.6 || data.EndTime != 2.0 {
		t.Fatalf("time range = %v-%v, want 1.6-2.0", data.StartTime, data.EndTime)
	}
	if data.Confidence != 0.9 {
		t.Fatalf("confidence = %f, want 0.9", data.Confidence)
	}
}

func TestSonioxProcessMessageEmitsInterimPreflightFinalAndUsage(t *testing.T) {
	state := &sonioxMessageState{}

	events, err := processSonioxMessage(state, []byte(`{"tokens":[{"text":"hello ","language":"en","is_final":true,"start_ms":0,"end_ms":100,"confidence":0.9},{"text":"wor","language":"en","is_final":false,"start_ms":100,"end_ms":200,"confidence":0.8}]}`))
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertSonioxEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertSonioxEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello wor")

	preflightState := &sonioxMessageState{}
	events, err = processSonioxMessage(preflightState, []byte(`{"tokens":[{"text":"hello ","language":"en","is_final":true,"start_ms":0,"end_ms":100,"confidence":0.9},{"text":"world","language":"en","is_final":true,"start_ms":100,"end_ms":250,"confidence":0.95}]}`))
	if err != nil {
		t.Fatalf("process preflight: %v", err)
	}
	assertSonioxEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertSonioxEvent(t, events, 1, stt.SpeechEventPreflightTranscript, "hello world")

	events, err = processSonioxMessage(preflightState, []byte(`{"tokens":[{"text":"<end>","is_final":true}],"total_audio_proc_ms":1250}`))
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertSonioxEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello world")
	assertSonioxEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
	if events[2].Type != stt.SpeechEventRecognitionUsage || events[2].RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("usage event = %+v, want 1.25s usage", events[2])
	}
}

func TestSonioxProcessMessageReturnsStatusError(t *testing.T) {
	_, err := processSonioxMessage(&sonioxMessageState{}, []byte(`{"error_code":"429","error_message":"rate limited","tokens":[]}`))
	if err == nil {
		t.Fatal("process message returned nil error, want API error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %q, want code and message", err.Error())
	}
}

func assertSonioxConfig(t *testing.T, config map[string]any, key string, want string) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func assertSonioxEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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

func anyFloat64(v float64) *float64 {
	return &v
}
