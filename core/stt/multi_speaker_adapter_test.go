package stt

import "testing"

func TestPrimarySpeakerDetectorFormatsPrimaryAndBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		false,
		"primary {speaker_id}: {text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	primary := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "hello",
			SpeakerID: "speaker-a",
		}},
	})
	if got := primary.Alternatives[0].Text; got != "primary speaker-a: hello" {
		t.Fatalf("primary text = %q, want formatted primary text", got)
	}
	if primary.Alternatives[0].IsPrimarySpeaker == nil || !*primary.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("primary IsPrimarySpeaker was not set to true")
	}

	background := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventInterimTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if got := background.Alternatives[0].Text; got != "background speaker-b: aside" {
		t.Fatalf("background text = %q, want formatted background text", got)
	}
	if background.Alternatives[0].IsPrimarySpeaker == nil || *background.Alternatives[0].IsPrimarySpeaker {
		t.Fatal("background IsPrimarySpeaker was not set to false")
	}
}

func TestPrimarySpeakerDetectorSuppressesFormattedBackgroundText(t *testing.T) {
	detector := newPrimarySpeakerDetector(
		true,
		true,
		"{text}",
		"background {speaker_id}: {text}",
		DefaultPrimarySpeakerDetectionOptions(),
	)
	detector.primarySpeaker = "speaker-a"

	event := detector.onSttEvent(&SpeechEvent{
		Type: SpeechEventFinalTranscript,
		Alternatives: []SpeechData{{
			Text:      "aside",
			SpeakerID: "speaker-b",
		}},
	})
	if event != nil {
		t.Fatalf("background event = %#v, want suppressed nil event", event)
	}
}
