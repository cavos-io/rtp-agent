package telnyx

import (
	"context"
	"encoding/binary"
	"net/url"
	"strings"
	"testing"

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
	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming interim offline recognize", caps)
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
