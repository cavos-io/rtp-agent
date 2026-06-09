package fireworksai

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestFireworksSTTDefaultsMatchReference(t *testing.T) {
	provider := NewFireworksSTT("test-key")

	if provider.baseURL != "wss://audio-streaming.us-virginia-1.direct.fireworks.ai/v1" {
		t.Fatalf("base URL = %q, want reference websocket base URL", provider.baseURL)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.textTimeoutSeconds != 1.0 {
		t.Fatalf("text timeout = %f, want 1.0", provider.textTimeoutSeconds)
	}
	if provider.responseFormat != "verbose_json" {
		t.Fatalf("response format = %q, want verbose_json", provider.responseFormat)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Fatal("streaming = false, want true")
	}
	if !caps.InterimResults {
		t.Fatal("interim results = false, want true")
	}
	if caps.OfflineRecognize {
		t.Fatal("offline recognize = true, want false")
	}
}

func TestNewFireworksSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FIREWORKS_API_KEY", "env-key")

	provider := NewFireworksSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewFireworksSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestFireworksSTTOptionsBuildReferenceStreamURLAndHeaders(t *testing.T) {
	provider := NewFireworksSTT("test-key",
		WithFireworksBaseURL("ws://fireworks.example/v1/"),
		WithFireworksModel("whisper-v3"),
		WithFireworksLanguage("en"),
		WithFireworksPrompt("names"),
		WithFireworksTemperature(0.2),
		WithFireworksSkipVAD(true),
		WithFireworksVADKwargs(map[string]any{"threshold": 0.15}),
		WithFireworksTextTimeoutSeconds(2.5),
		WithFireworksTimestampGranularities([]string{"word", "segment"}),
	)

	streamURL, err := url.Parse(buildFireworksStreamURL(provider))
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}

	if !strings.HasPrefix(streamURL.String(), "ws://fireworks.example/v1/audio_streaming") {
		t.Fatalf("url = %q, want audio_streaming endpoint", streamURL.String())
	}
	query := streamURL.Query()
	assertFireworksQuery(t, query, "model", "whisper-v3")
	assertFireworksQuery(t, query, "language", "en")
	assertFireworksQuery(t, query, "prompt", "names")
	assertFireworksQuery(t, query, "temperature", "0.2")
	assertFireworksQuery(t, query, "skip_vad", "true")
	assertFireworksQuery(t, query, "text_timeout_seconds", "2.5")
	assertFireworksQuery(t, query, "response_format", "verbose_json")
	if got := query["timestamp_granularities"]; len(got) != 2 || got[0] != "word" || got[1] != "segment" {
		t.Fatalf("timestamp_granularities = %#v, want word/segment", got)
	}
	if got := query.Get("vad_kwargs"); !strings.Contains(got, `"threshold":0.15`) {
		t.Fatalf("vad_kwargs = %q, want encoded threshold JSON", got)
	}

	headers := buildFireworksStreamHeaders(provider)
	if headers.Get("Authorization") != "test-key" {
		t.Fatalf("Authorization = %q, want raw API key", headers.Get("Authorization"))
	}
	if headers.Get("User-Agent") != "LiveKit Agents" {
		t.Fatalf("User-Agent = %q, want LiveKit Agents", headers.Get("User-Agent"))
	}
}

func TestFireworksSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewFireworksSTT("test-key")

	_, err := provider.Recognize(context.Background(), []*model.AudioFrame{{Data: []byte("pcm")}}, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "does not support batch recognition") {
		t.Fatalf("Recognize error = %q, want reference unsupported error", err.Error())
	}
}

func TestFireworksProcessStreamEventEmitsStartInterimFinalAndEnd(t *testing.T) {
	state := &fireworksStreamState{language: "en", lastFinalSegmentID: -1}

	events := processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{ID: 0, Text: "hello"},
		},
	}, false)

	assertFireworksEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertFireworksEvent(t, events, 1, stt.SpeechEventInterimTranscript, "hello")

	events = processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{ID: 0, Text: "hello world", Words: []fireworksWord{{Word: "world", IsFinal: true}}},
		},
	}, true)

	assertFireworksEvent(t, events, 0, stt.SpeechEventFinalTranscript, "hello world")
	assertFireworksEvent(t, events, 1, stt.SpeechEventEndOfSpeech, "")
}

func TestFireworksProcessStreamEventKeepsNewWordsAfterFinalSegment(t *testing.T) {
	state := &fireworksStreamState{
		language:            "en",
		lastFinalSegmentID:  1,
		finalSegmentsLength: map[int]int{1: 2},
	}

	events := processFireworksStreamEvent(state, fireworksStreamEvent{
		Segments: []fireworksSegment{
			{
				ID: 1,
				Words: []fireworksWord{
					{Word: "old"},
					{Word: "words"},
					{Word: "new"},
					{Word: "tail"},
				},
			},
		},
	}, false)

	assertFireworksEvent(t, events, 0, stt.SpeechEventStartOfSpeech, "")
	assertFireworksEvent(t, events, 1, stt.SpeechEventInterimTranscript, "new tail")
}

func assertFireworksQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func assertFireworksEvent(t *testing.T, events []*stt.SpeechEvent, index int, eventType stt.SpeechEventType, text string) {
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
	if events[index].Alternatives[0].Language != "en" {
		t.Fatalf("event %d language = %q, want en", index, events[index].Alternatives[0].Language)
	}
}
