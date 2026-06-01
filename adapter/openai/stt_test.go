package openai

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
	goopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAIAudioRequestAsksForWordTimestamps(t *testing.T) {
	provider := NewOpenAISTT("test-key", "whisper-1")
	req := openAIAudioRequest(provider, strings.NewReader("audio"), "en")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "en" {
		t.Fatalf("language = %q, want en", req.Language)
	}
	if req.Format != goopenai.AudioResponseFormatVerboseJSON {
		t.Fatalf("format = %q, want verbose_json", req.Format)
	}
	if len(req.TimestampGranularities) != 1 || req.TimestampGranularities[0] != goopenai.TranscriptionTimestampGranularityWord {
		t.Fatalf("timestamp granularities = %#v, want word", req.TimestampGranularities)
	}
}

func TestOpenAISpeechEventPreservesWordTimestamps(t *testing.T) {
	var resp goopenai.AudioResponse
	if err := json.Unmarshal([]byte(`{
		"text": "hello world",
		"words": [
			{"word": "hello", "start": 0.1, "end": 0.3},
			{"word": "world", "start": 0.4, "end": 0.8}
		]
	}`), &resp); err != nil {
		t.Fatal(err)
	}

	event := openAISpeechEvent(resp)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || math.Abs(got.StartTime-0.1) > 0.000001 || math.Abs(got.EndTime-0.3) > 0.000001 {
		t.Fatalf("first word = %+v, want hello timing", got)
	}
	if got := alt.Words[1]; got.Text != "world" || math.Abs(got.StartTime-0.4) > 0.000001 || math.Abs(got.EndTime-0.8) > 0.000001 {
		t.Fatalf("second word = %+v, want world timing", got)
	}
}

func TestOpenAISTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := NewOpenAISTT("test-key", "whisper-1")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestOpenAISTTDefaultsMatchReference(t *testing.T) {
	provider := NewOpenAISTT("test-key", "")

	if provider.model != "gpt-4o-mini-transcribe" {
		t.Fatalf("model = %q, want gpt-4o-mini-transcribe", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
}

func TestOpenAIAudioRequestUsesProviderOptions(t *testing.T) {
	provider := NewOpenAISTT("test-key", "whisper-1",
		WithOpenAISTTLanguage("id"),
		WithOpenAISTTPrompt("domain words"),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Model != "whisper-1" {
		t.Fatalf("model = %q, want whisper-1", req.Model)
	}
	if req.Language != "id" {
		t.Fatalf("language = %q, want id", req.Language)
	}
	if req.Prompt != "domain words" {
		t.Fatalf("prompt = %q, want domain words", req.Prompt)
	}
}

func TestOpenAISTTDetectLanguageOmitsLanguage(t *testing.T) {
	provider := NewOpenAISTT("test-key", "",
		WithOpenAISTTDetectLanguage(true),
	)

	req := openAIAudioRequest(provider, strings.NewReader("audio"), "")

	if req.Language != "" {
		t.Fatalf("language = %q, want empty for language detection", req.Language)
	}
}
