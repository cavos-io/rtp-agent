package speechmatics

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestSpeechmaticsTranscriptEventPreservesWordTimings(t *testing.T) {
	resp := smResponse{
		Message: "AddTranscript",
		Results: []struct {
			Alternatives []struct {
				Content    string  `json:"content"`
				Confidence float64 `json:"confidence"`
			} `json:"alternatives"`
			Type      string  `json:"type"`
			StartTime float64 `json:"start_time"`
			EndTime   float64 `json:"end_time"`
		}{
			{
				Type:      "word",
				StartTime: 0.1,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: "hello", Confidence: 0.92}},
			},
			{
				Type:      "punctuation",
				StartTime: 0.3,
				EndTime:   0.3,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: ",", Confidence: 1.0}},
			},
			{
				Type:      "word",
				StartTime: 0.4,
				EndTime:   0.8,
				Alternatives: []struct {
					Content    string  `json:"content"`
					Confidence float64 `json:"confidence"`
				}{{Content: "world", Confidence: 0.88}},
			},
		},
	}

	event := speechmaticsTranscriptEvent(resp)
	if event == nil {
		t.Fatal("speechmaticsTranscriptEvent returned nil")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event.Type = %s, want final_transcript", event.Type)
	}
	if got := event.Alternatives[0].Text; got != "hello, world" {
		t.Fatalf("text = %q, want punctuation-formatted transcript", got)
	}
	words := event.Alternatives[0].Words
	if len(words) != 2 {
		t.Fatalf("words = %#v, want two timed words", words)
	}
	if words[0].Text != "hello" || words[0].StartTime != 0.1 || words[0].EndTime != 0.3 || words[0].Confidence != 0.92 {
		t.Fatalf("first word = %#v, want preserved word timing", words[0])
	}
	if words[1].Text != "world" || words[1].StartTime != 0.4 || words[1].EndTime != 0.8 || words[1].Confidence != 0.88 {
		t.Fatalf("second word = %#v, want preserved word timing", words[1])
	}
}

func TestSpeechmaticsSTTCapabilitiesMatchReference(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")
	capabilities := provider.Capabilities()

	if !capabilities.Streaming {
		t.Fatal("Streaming = false, want true")
	}
	if !capabilities.InterimResults {
		t.Fatal("InterimResults = false, want true")
	}
	if !capabilities.Diarization {
		t.Fatal("Diarization = false, want true")
	}
	if capabilities.AlignedTranscript != "chunk" {
		t.Fatalf("AlignedTranscript = %q, want chunk", capabilities.AlignedTranscript)
	}
	if capabilities.OfflineRecognize {
		t.Fatal("OfflineRecognize = true, want false")
	}
}

func TestSpeechmaticsSTTRecognizeMatchesReferenceUnsupportedOffline(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key")

	_, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want unsupported offline recognition")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("Recognize error = %q, want not implemented", err.Error())
	}
}

func TestSpeechmaticsSTTStartMessageUsesReferenceOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTLanguage("de"),
		WithSpeechmaticsSTTSampleRate(48000),
		WithSpeechmaticsSTTAudioEncoding("pcm_f32le"),
		WithSpeechmaticsSTTDomain("finance"),
		WithSpeechmaticsSTTOutputLocale("de-DE"),
		WithSpeechmaticsSTTIncludePartials(false),
		WithSpeechmaticsSTTEnableDiarization(false),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	if message["message"] != "StartRecognition" {
		t.Fatalf("message = %#v, want StartRecognition", message["message"])
	}
	audioFormat := message["audio_format"].(map[string]interface{})
	if audioFormat["sample_rate"] != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", audioFormat["sample_rate"])
	}
	if audioFormat["encoding"] != "pcm_f32le" {
		t.Fatalf("encoding = %#v, want pcm_f32le", audioFormat["encoding"])
	}
	config := message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "de")
	assertSpeechmaticsConfig(t, config, "domain", "finance")
	assertSpeechmaticsConfig(t, config, "output_locale", "de-DE")
	assertSpeechmaticsConfig(t, config, "enable_partials", false)
	assertSpeechmaticsConfig(t, config, "diarization", "none")

	message = buildSpeechmaticsSTTStartMessage(provider, "fr")
	config = message["transcription_config"].(map[string]interface{})
	assertSpeechmaticsConfig(t, config, "language", "fr")

	if _, err := json.Marshal(message); err != nil {
		t.Fatalf("marshal start message: %v", err)
	}
}

func TestSpeechmaticsSTTStartMessageUsesVocabularyAndSpeakerOptions(t *testing.T) {
	provider := NewSpeechmaticsSTT("test-key",
		WithSpeechmaticsSTTAdditionalVocab([]SpeechmaticsAdditionalVocabEntry{
			{Content: "LiveKit", SoundsLike: []string{"live kit"}},
			{Content: "Cavos"},
		}),
		WithSpeechmaticsSTTSpeakerFocus([]string{"agent"}, []string{"customer"}, "ignore"),
		WithSpeechmaticsSTTKnownSpeakers([]SpeechmaticsSpeakerIdentifier{
			{Label: "agent", SpeakerID: "spk-1"},
		}),
	)

	message := buildSpeechmaticsSTTStartMessage(provider, "")
	config := message["transcription_config"].(map[string]interface{})

	vocab := config["additional_vocab"].([]SpeechmaticsAdditionalVocabEntry)
	if len(vocab) != 2 || vocab[0].Content != "LiveKit" || vocab[0].SoundsLike[0] != "live kit" {
		t.Fatalf("additional_vocab = %#v, want LiveKit sounds-like entry", vocab)
	}
	speakerConfig := config["speaker_config"].(map[string]interface{})
	if got := speakerConfig["focus_speakers"].([]string); len(got) != 1 || got[0] != "agent" {
		t.Fatalf("focus_speakers = %#v, want agent", got)
	}
	if got := speakerConfig["ignore_speakers"].([]string); len(got) != 1 || got[0] != "customer" {
		t.Fatalf("ignore_speakers = %#v, want customer", got)
	}
	if speakerConfig["focus_mode"] != "ignore" {
		t.Fatalf("focus_mode = %#v, want ignore", speakerConfig["focus_mode"])
	}
	knownSpeakers := config["known_speakers"].([]SpeechmaticsSpeakerIdentifier)
	if len(knownSpeakers) != 1 || knownSpeakers[0].Label != "agent" || knownSpeakers[0].SpeakerID != "spk-1" {
		t.Fatalf("known_speakers = %#v, want agent speaker id", knownSpeakers)
	}
}

func assertSpeechmaticsConfig(t *testing.T, config map[string]interface{}, key string, want interface{}) {
	t.Helper()
	if got := config[key]; got != want {
		t.Fatalf("%s = %#v, want %#v in %#v", key, got, want, config)
	}
}
