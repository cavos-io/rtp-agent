package deepgram

import (
	"testing"

	"github.com/cavos-io/conversation-worker/core/stt"
)

func TestDeepgramSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 2
	resp := dgResponse{
		Type:     "Results",
		IsFinal:  true,
		Start:    1.5,
		Duration: 0.9,
	}
	resp.Metadata.RequestID = "request-1"
	resp.Channel.Alternatives = []dgAlternative{
		{
			Transcript: "hello world",
			Confidence: 0.98,
			Words: []dgWord{
				{Word: "hello", Start: 1.5, End: 1.8, Confidence: 0.99, Speaker: &speaker},
				{Word: "world", Start: 1.9, End: 2.4, Confidence: 0.97, Speaker: &speaker},
			},
		},
	}

	event := deepgramSpeechEvent(resp)
	if event == nil {
		t.Fatal("expected speech event")
	}
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if event.RequestID != "request-1" {
		t.Fatalf("request id = %q, want request-1", event.RequestID)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", alt.Text)
	}
	if alt.StartTime != 1.5 || alt.EndTime != 2.4 {
		t.Fatalf("time range = %v-%v, want 1.5-2.4", alt.StartTime, alt.EndTime)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 1.5 || got.EndTime != 1.8 || got.Confidence != 0.99 || got.SpeakerID != "2" {
		t.Fatalf("first word = %+v, want hello timing with speaker 2", got)
	}
	if got := alt.Words[1]; got.Text != "world" || got.StartTime != 1.9 || got.EndTime != 2.4 || got.Confidence != 0.97 || got.SpeakerID != "2" {
		t.Fatalf("second word = %+v, want world timing with speaker 2", got)
	}
}

func TestDeepgramRecognizeSpeechEventPreservesAlternativeWords(t *testing.T) {
	speaker := 0
	resp := dgRecognitionResponse{}
	resp.Results.Channels = []dgRecognitionChannel{
		{
			Alternatives: []dgAlternative{
				{
					Transcript: "hello offline",
					Confidence: 0.91,
					Words: []dgWord{
						{Word: "hello", Start: 0.1, End: 0.3, Confidence: 0.93, Speaker: &speaker},
						{Word: "offline", Start: 0.4, End: 0.8, Confidence: 0.9, Speaker: &speaker},
					},
				},
			},
		},
	}

	event := deepgramRecognizeSpeechEvent(resp)
	if event.Type != stt.SpeechEventFinalTranscript {
		t.Fatalf("event type = %v, want %v", event.Type, stt.SpeechEventFinalTranscript)
	}
	if len(event.Alternatives) != 1 {
		t.Fatalf("alternatives = %d, want 1", len(event.Alternatives))
	}

	alt := event.Alternatives[0]
	if alt.Text != "hello offline" {
		t.Fatalf("text = %q, want hello offline", alt.Text)
	}
	if len(alt.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(alt.Words))
	}
	if got := alt.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.93 || got.SpeakerID != "0" {
		t.Fatalf("first word = %+v, want hello timing with speaker 0", got)
	}
	if got := alt.Words[1]; got.Text != "offline" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.9 || got.SpeakerID != "0" {
		t.Fatalf("second word = %+v, want offline timing with speaker 0", got)
	}
}

func TestDeepgramSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := NewDeepgramSTT("test-key", "nova-2")

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}
