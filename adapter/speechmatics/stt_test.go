package speechmatics

import (
	"context"
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
