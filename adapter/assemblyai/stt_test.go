package assemblyai

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

func TestAssemblyAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.speechModel != "universal-streaming-english" {
		t.Fatalf("speech model = %q, want universal-streaming-english", provider.speechModel)
	}
	if provider.baseURL != "wss://streaming.assemblyai.com" {
		t.Fatalf("base URL = %q, want streaming endpoint", provider.baseURL)
	}
	if provider.minTurnSilence == nil || *provider.minTurnSilence != 100 {
		t.Fatalf("min turn silence = %v, want 100", provider.minTurnSilence)
	}
}

func TestAssemblyAIStreamURLUsesReferenceDefaults(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	streamURL := buildAssemblyAIStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "streaming.assemblyai.com" || parsed.Path != "/v3/ws" {
		t.Fatalf("stream URL = %s, want wss://streaming.assemblyai.com/v3/ws", streamURL)
	}

	query := parsed.Query()
	assertAssemblyAIQuery(t, query, "sample_rate", "16000")
	assertAssemblyAIQuery(t, query, "encoding", "pcm_s16le")
	assertAssemblyAIQuery(t, query, "speech_model", "universal-streaming-english")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "language_detection", "false")
	if query.Has("max_turn_silence") {
		t.Fatalf("max_turn_silence = %q, want omitted for default english model", query.Get("max_turn_silence"))
	}
}

func TestAssemblyAIStreamURLUsesReferenceU3Defaults(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-rt-pro"))

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))

	assertAssemblyAIQuery(t, query, "speech_model", "u3-rt-pro")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "max_turn_silence", "100")
	assertAssemblyAIQuery(t, query, "continuous_partials", "true")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
}

func TestAssemblyAIStreamURLNormalizesDeprecatedU3ProModel(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTModel("u3-pro"))

	if provider.speechModel != "u3-rt-pro" {
		t.Fatalf("speech model = %q, want u3-rt-pro", provider.speechModel)
	}

	query := mustAssemblyAIStreamQuery(t, buildAssemblyAIStreamURL(provider))
	assertAssemblyAIQuery(t, query, "speech_model", "u3-rt-pro")
	assertAssemblyAIQuery(t, query, "continuous_partials", "true")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
}

func TestAssemblyAIStreamURLEncodesReferenceOptions(t *testing.T) {
	provider := NewAssemblyAISTT("test-key",
		WithAssemblyAISTTBaseURL("wss://streaming.eu.assemblyai.com"),
		WithAssemblyAISTTSampleRate(8000),
		WithAssemblyAISTTMinTurnSilence(250),
		WithAssemblyAISTTMaxTurnSilence(900),
		WithAssemblyAISTTLanguageDetection(true),
		WithAssemblyAISTTSpeakerLabels(true),
	)

	streamURL := buildAssemblyAIStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	if parsed.Host != "streaming.eu.assemblyai.com" {
		t.Fatalf("host = %q, want EU streaming endpoint", parsed.Host)
	}

	query := parsed.Query()
	assertAssemblyAIQuery(t, query, "sample_rate", "8000")
	assertAssemblyAIQuery(t, query, "min_turn_silence", "250")
	assertAssemblyAIQuery(t, query, "max_turn_silence", "900")
	assertAssemblyAIQuery(t, query, "language_detection", "true")
	assertAssemblyAIQuery(t, query, "speaker_labels", "true")
}

func TestAssemblyAIRealtimeTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := aaiResponse{
		Type:       "Turn",
		Transcript: "hello realtime",
		EndOfTurn:  true,
		Words: []assemblyAIWord{
			{Text: "hello", Start: 100, End: 300, Confidence: 0.95},
			{Text: "realtime", Start: 350, End: 800, Confidence: 0.9},
		},
	}

	event := assemblyAIRealtimeTranscriptEvent(resp)
	if event == nil {
		t.Fatal("expected realtime transcript event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final transcript", event.Type)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello realtime" {
		t.Fatalf("text = %q, want hello realtime", alt.Text)
	}
	if alt.Confidence != 0.925 {
		t.Fatalf("confidence = %v, want average word confidence", alt.Confidence)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %#v, want two timed words", alt.Words)
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.95 {
		t.Fatalf("first word = %#v, want converted AssemblyAI realtime word timing", got)
	}
	if got := alt.Words[1]; got.Text != "realtime" || got.StartTime != 0.35 || got.EndTime != 0.8 || got.Confidence != 0.9 {
		t.Fatalf("second word = %#v, want converted AssemblyAI realtime word timing", got)
	}
}

func TestAssemblyAISTTCapabilitiesMatchReference(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")
	capabilities := provider.Capabilities()

	if !capabilities.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if !capabilities.InterimResults {
		t.Fatal("InterimResults = false, want true")
	}
	if capabilities.AlignedTranscript != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", capabilities.AlignedTranscript)
	}
	if capabilities.OfflineRecognize {
		t.Fatal("OfflineRecognize = true, want false")
	}
}

func TestAssemblyAISTTCapabilitiesEnableDiarizationFromSpeakerLabels(t *testing.T) {
	provider := NewAssemblyAISTT("test-key", WithAssemblyAISTTSpeakerLabels(true))

	if !provider.Capabilities().Diarization {
		t.Fatal("Diarization = false, want true when speaker labels are enabled")
	}
}

func TestAssemblyAISTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewAssemblyAISTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Recognize error = %q, want not implemented", err.Error())
	}
}

func assertAssemblyAIQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func mustAssemblyAIStreamQuery(t *testing.T, streamURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream URL: %v", err)
	}
	return parsed.Query()
}
