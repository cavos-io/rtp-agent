package gladia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestGladiaSTTDefaultsMatchReferenceV2(t *testing.T) {
	provider := NewGladiaSTT("test-key")

	if provider.baseURL != "https://api.gladia.io/v2/live" {
		t.Fatalf("base URL = %q, want v2 live endpoint", provider.baseURL)
	}
	if provider.model != "solaria-1" {
		t.Fatalf("model = %q, want solaria-1", provider.model)
	}
	if got := stt.Model(provider); got != "solaria-1" {
		t.Fatalf("model metadata = %q, want solaria-1", got)
	}
	if got := stt.Provider(provider); got != "Gladia" {
		t.Fatalf("provider metadata = %q, want Gladia", got)
	}
	if provider.sampleRate != 16000 || provider.bitDepth != 16 || provider.channels != 1 {
		t.Fatalf("audio config = %d/%d/%d, want 16000/16/1", provider.sampleRate, provider.bitDepth, provider.channels)
	}
	if provider.region != "eu-west" || provider.encoding != "wav/pcm" {
		t.Fatalf("region/encoding = %q/%q, want eu-west/wav/pcm", provider.region, provider.encoding)
	}
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || caps.AlignedTranscript != "word" || caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming interim word-aligned only", caps)
	}
}

func TestGladiaSTTUsesEnvAPIKeyWhenOmitted(t *testing.T) {
	t.Setenv("GLADIA_API_KEY", "env-key")

	provider := NewGladiaSTT("")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewGladiaSTT("explicit-key")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestGladiaSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("GLADIA_API_KEY", "")
	provider := NewGladiaSTT("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Stream(ctx, "")
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GLADIA_API_KEY") {
		t.Fatalf("Stream error = %q, want GLADIA_API_KEY guidance", err)
	}
}

func TestBuildGladiaStreamingConfigMatchesReference(t *testing.T) {
	provider := NewGladiaSTT("test-key",
		WithGladiaLanguages([]string{"en", "fr"}),
		WithGladiaCodeSwitching(false),
		WithGladiaCustomVocabulary([]any{"LiveKit", map[string]any{"value": "Cavos"}}),
		WithGladiaCustomSpelling(map[string][]string{
			"livekit": {"LiveKit", "Live Kit"},
			"cavos":   {"Cavos"},
		}),
		WithGladiaTranslation([]string{"es"}),
		WithGladiaPreProcessing(true, 0.7),
	)

	config := buildGladiaStreamingConfig(provider)
	assertGladiaField(t, config, "region", "eu-west")
	assertGladiaField(t, config, "encoding", "wav/pcm")
	assertGladiaField(t, config, "sample_rate", 16000)
	assertGladiaField(t, config, "model", "solaria-1")

	languageConfig := config["language_config"].(map[string]any)
	if languages := languageConfig["languages"].([]string); len(languages) != 2 || languages[0] != "en" || languages[1] != "fr" {
		t.Fatalf("languages = %+v, want en/fr", languages)
	}
	if languageConfig["code_switching"] != false {
		t.Fatalf("code_switching = %#v, want false", languageConfig["code_switching"])
	}
	realtime := config["realtime_processing"].(map[string]any)
	if realtime["words_accurate_timestamps"] != false || realtime["custom_vocabulary"] != true || realtime["translation"] != true {
		t.Fatalf("realtime = %+v, want timestamps false with custom vocab and translation", realtime)
	}
	if realtime["custom_spelling"] != true {
		t.Fatalf("custom_spelling = %#v, want true", realtime["custom_spelling"])
	}
	customSpellingConfig := realtime["custom_spelling_config"].(map[string]any)
	spellingDictionary := customSpellingConfig["spelling_dictionary"].(map[string][]string)
	if got := spellingDictionary["livekit"]; len(got) != 2 || got[0] != "LiveKit" || got[1] != "Live Kit" {
		t.Fatalf("livekit spelling = %+v, want LiveKit/Live Kit", got)
	}
	messages := config["messages_config"].(map[string]any)
	if messages["receive_partial_transcripts"] != true || messages["receive_final_transcripts"] != true {
		t.Fatalf("messages = %+v, want partial/final transcripts", messages)
	}
	pre := config["pre_processing"].(map[string]any)
	if pre["audio_enhancer"] != true || pre["speech_threshold"] != 0.7 {
		t.Fatalf("pre_processing = %+v, want enhancer threshold", pre)
	}
}

func TestGladiaSTTConfigOptionsMatchReference(t *testing.T) {
	provider := NewGladiaSTT("test-key",
		WithGladiaModel("solaria-1-large"),
		WithGladiaInterimResults(false),
		WithGladiaAudioFormat(48000, 24, 2, "wav/alaw"),
		WithGladiaEndpointing(0.2, 8.5),
		WithGladiaRegion("us-west"),
	)

	config := buildGladiaStreamingConfig(provider)
	assertGladiaField(t, config, "model", "solaria-1-large")
	assertGladiaField(t, config, "sample_rate", 48000)
	assertGladiaField(t, config, "bit_depth", 24)
	assertGladiaField(t, config, "channels", 2)
	assertGladiaField(t, config, "encoding", "wav/alaw")
	assertGladiaField(t, config, "endpointing", 0.2)
	assertGladiaField(t, config, "maximum_duration_without_endpointing", 8.5)
	assertGladiaField(t, config, "region", "us-west")

	messages := config["messages_config"].(map[string]any)
	if messages["receive_partial_transcripts"] != false || provider.Capabilities().InterimResults {
		t.Fatalf("interim results = message:%#v capability:%t, want disabled", messages["receive_partial_transcripts"], provider.Capabilities().InterimResults)
	}
}

func TestGladiaTranslationConfigOptionsMatchReference(t *testing.T) {
	provider := NewGladiaSTT("test-key",
		WithGladiaTranslationConfig([]string{"es", "fr"}, "enhanced", false, false, true, "medical appointment", true),
	)

	config := buildGladiaStreamingConfig(provider)
	realtime := config["realtime_processing"].(map[string]any)
	if realtime["translation"] != true {
		t.Fatalf("translation = %#v, want true", realtime["translation"])
	}
	translationConfig := realtime["translation_config"].(map[string]any)
	targetLanguages := translationConfig["target_languages"].([]string)
	if len(targetLanguages) != 2 || targetLanguages[0] != "es" || targetLanguages[1] != "fr" {
		t.Fatalf("target_languages = %+v, want es/fr", targetLanguages)
	}
	assertGladiaField(t, translationConfig, "model", "enhanced")
	assertGladiaField(t, translationConfig, "match_original_utterances", false)
	assertGladiaField(t, translationConfig, "lipsync", false)
	assertGladiaField(t, translationConfig, "context_adaptation", true)
	assertGladiaField(t, translationConfig, "context", "medical appointment")
	assertGladiaField(t, translationConfig, "informal", true)
}

func TestBuildGladiaInitRequestMovesRegionToQuery(t *testing.T) {
	provider := NewGladiaSTT("test-key", WithGladiaBaseURL("https://gladia.example/v2/live"))
	req, err := buildGladiaInitRequest(context.Background(), provider)
	if err != nil {
		t.Fatalf("build init request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	parsed, err := url.Parse(req.URL.String())
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "gladia.example" || parsed.Path != "/v2/live" {
		t.Fatalf("URL = %q, want configured init endpoint", req.URL.String())
	}
	if parsed.Query().Get("region") != "eu-west" {
		t.Fatalf("region query = %q, want eu-west", parsed.Query().Get("region"))
	}
	if req.Header.Get("X-Gladia-Key") != "test-key" {
		t.Fatalf("X-Gladia-Key = %q, want key", req.Header.Get("X-Gladia-Key"))
	}

	var body map[string]any
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["region"]; ok {
		t.Fatalf("body still contains region: %+v", body)
	}
}

func TestGladiaAudioMessagesUseV2Schema(t *testing.T) {
	audioMessage := buildGladiaAudioChunkMessage([]byte{1, 2, 3})
	if audioMessage["type"] != "audio_chunk" {
		t.Fatalf("type = %q, want audio_chunk", audioMessage["type"])
	}
	data := audioMessage["data"].(map[string]any)
	if data["chunk"] != base64.StdEncoding.EncodeToString([]byte{1, 2, 3}) {
		t.Fatalf("chunk = %q, want base64 audio", data["chunk"])
	}
	stop := buildGladiaStopRecordingMessage()
	if stop["type"] != "stop_recording" {
		t.Fatalf("stop type = %q, want stop_recording", stop["type"])
	}
}

func TestGladiaTranscriptEventsMatchReferenceLifecycle(t *testing.T) {
	state := &gladiaSTTStreamState{requestID: "session-1", languages: []string{"en"}}
	events, err := processGladiaMessage(state, map[string]any{
		"type": "transcript",
		"data": map[string]any{
			"is_final": false,
			"utterance": map[string]any{
				"text":       "hello",
				"start":      0.1,
				"end":        0.4,
				"confidence": 0.8,
				"language":   "en",
				"words":      []any{map[string]any{"word": "hello", "start": 0.1, "end": 0.4}},
			},
		},
	})
	if err != nil {
		t.Fatalf("process interim: %v", err)
	}
	assertGladiaEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertGladiaEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")

	events, err = processGladiaMessage(state, map[string]any{
		"type": "transcript",
		"data": map[string]any{
			"is_final": true,
			"utterance": map[string]any{
				"text":       "hello final",
				"start":      0.1,
				"end":        0.5,
				"confidence": 0.9,
			},
		},
	})
	if err != nil {
		t.Fatalf("process final: %v", err)
	}
	assertGladiaEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello final")
	assertGladiaEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func assertGladiaField(t *testing.T, config map[string]any, key string, want any) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func assertGladiaEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
