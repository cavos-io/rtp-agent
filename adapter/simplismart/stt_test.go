package simplismart

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestSimplismartSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key")

	if provider.baseURL != "https://api.simplismart.live/predict" {
		t.Fatalf("base URL = %q, want reference predict endpoint", provider.baseURL)
	}
	if provider.model != "openai/whisper-large-v3-turbo" {
		t.Fatalf("model = %q, want reference default model", provider.model)
	}
	if got := stt.Model(provider); got != "openai/whisper-large-v3-turbo" {
		t.Fatalf("model metadata = %q, want reference default model", got)
	}
	if got := stt.Provider(provider); got != "Simplismart" {
		t.Fatalf("provider metadata = %q, want Simplismart", got)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.task != "transcribe" {
		t.Fatalf("task = %q, want transcribe", provider.task)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}

	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatal("streaming = true, want false by default")
	}
	if caps.AlignedTranscript != "word" {
		t.Fatalf("aligned transcript = %q, want word", caps.AlignedTranscript)
	}
	if !caps.OfflineRecognize {
		t.Fatal("offline recognize = false, want true")
	}
}

func TestNewSimplismartSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "env-key")

	provider := NewSimplismartSTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSimplismartSTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSimplismartSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	provider := NewSimplismartSTT("")
	_, err := provider.Recognize(ctx, nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Recognize error = %q, want SIMPLISMART_API_KEY guidance", err)
	}

	streamingProvider := NewSimplismartSTT("", WithSimplismartSTTStreaming(true))
	_, err = streamingProvider.Stream(ctx, "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Stream error = %q, want SIMPLISMART_API_KEY guidance", err)
	}
}

func TestSimplismartSTTStreamingModeMatchesReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key", WithSimplismartSTTStreaming(true))

	if provider.baseURL != "wss://api.simplismart.live/ws/audio" {
		t.Fatalf("base URL = %q, want websocket audio endpoint", provider.baseURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want true when streaming enabled")
	}
}

func TestSimplismartSTTOptionsMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTBaseURL("https://simplismart.example/predict"),
		WithSimplismartSTTModel("custom/model"),
		WithSimplismartSTTLanguage("fr"),
		WithSimplismartSTTTask("translate"),
		WithSimplismartSTTWithoutTimestamps(false),
		WithSimplismartSTTHotwords("Chicago,Joplin"),
		WithSimplismartSTTNumSpeakers(2),
	)

	if provider.baseURL != "https://simplismart.example/predict" {
		t.Fatalf("base URL = %q, want custom predict endpoint", provider.baseURL)
	}
	if provider.model != "custom/model" || provider.language != "fr" || provider.task != "translate" {
		t.Fatalf("provider = %+v, want custom model/language/task", provider)
	}
	if provider.withoutTimestamps || provider.hotwords != "Chicago,Joplin" || provider.numSpeakers != 2 {
		t.Fatalf("provider = %+v, want custom recognition options", provider)
	}
}

func TestSimplismartSTTRecognizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTModel("custom/model"),
		WithSimplismartSTTLanguage("fr"),
		WithSimplismartSTTHotwords("Chicago,Joplin"),
	)

	req, err := buildSimplismartSTTRecognizeRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != "POST" {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.simplismart.live/predict" {
		t.Fatalf("url = %q, want predict endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartPayload(t, payload, "audio_data", "AQI=")
	assertSimplismartPayload(t, payload, "language", "fr")
	assertSimplismartPayload(t, payload, "model", "custom/model")
	assertSimplismartPayload(t, payload, "task", "transcribe")
	assertSimplismartPayload(t, payload, "hotwords", "Chicago,Joplin")
	if payload["without_timestamps"] != true {
		t.Fatalf("without_timestamps = %#v, want true", payload["without_timestamps"])
	}
}

func TestSimplismartSTTRecognizeLanguageOverride(t *testing.T) {
	provider := NewSimplismartSTT("test-key", WithSimplismartSTTLanguage("fr"))

	req, err := buildSimplismartSTTRecognizeRequest(context.Background(), provider, []byte{0x01}, "de")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartPayload(t, payload, "language", "de")
}

func TestSimplismartSTTRecognizeResponseMapsReferenceShape(t *testing.T) {
	event := simplismartSTTSpeechEvent("fr", simplismartSTTResponse{
		RequestID:     "req-1",
		Transcription: []string{"bonjour ", "monde"},
		Timestamps:    [][2]float64{{0.2, 0.7}, {0.8, 1.1}},
		Info: simplismartSTTInfo{
			Language: "fr",
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("type = %v, want final transcript", event.Type)
	}
	if event.RequestID != "req-1" {
		t.Fatalf("request id = %q, want req-1", event.RequestID)
	}
	alt := event.Alternatives[0]
	if alt.Text != "bonjour monde" || alt.Language != "fr" {
		t.Fatalf("alt = %+v, want French transcript", alt)
	}
	if alt.StartTime != 0.2 || alt.EndTime != 1.1 {
		t.Fatalf("time range = %v-%v, want timestamp span", alt.StartTime, alt.EndTime)
	}
}

func TestSimplismartSTTStreamURLHeadersAndConfigMatchReference(t *testing.T) {
	provider := NewSimplismartSTT("test-key",
		WithSimplismartSTTBaseURL("https://simplismart.example/predict"),
		WithSimplismartSTTStreaming(true),
		WithSimplismartSTTLanguage("fr"),
	)

	streamURL, err := url.Parse(buildSimplismartSTTStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if got := streamURL.String(); got != "wss://simplismart.example/ws/audio" {
		t.Fatalf("stream URL = %q, want websocket audio URL", got)
	}

	headers := buildSimplismartSTTHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}

	config, err := buildSimplismartSTTInitialConfig("de")
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(config, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	assertSimplismartPayload(t, payload, "language", "de")
}

func TestSimplismartSTTStreamTranscriptEvents(t *testing.T) {
	events := simplismartSTTStreamEvents("req-1", "fr", []byte("bonjour"))
	if len(events) != 2 {
		t.Fatalf("events = %d, want usage and final transcript", len(events))
	}
	if events[0].Type != stt.SpeechEventRecognitionUsage {
		t.Fatalf("first event type = %v, want recognition usage", events[0].Type)
	}
	if events[1].Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("second event type = %v, want final transcript", events[1].Type)
	}
	if events[1].RequestID != "req-1" || events[1].Alternatives[0].Text != "bonjour" {
		t.Fatalf("final event = %+v, want request transcript", events[1])
	}
}

func assertSimplismartPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}
