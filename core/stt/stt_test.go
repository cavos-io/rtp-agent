package stt

import "testing"

func TestSpeechDataCarriesReferenceMetadataFields(t *testing.T) {
	word := TimedString{
		Text:            "hello",
		StartTime:       0.1,
		EndTime:         0.4,
		Confidence:      0.95,
		StartTimeOffset: 1.2,
		SpeakerID:       "speaker-a",
	}
	data := SpeechData{
		Language:        "en",
		Text:            "hello",
		Words:           []TimedString{word},
		SourceLanguages: []string{"en-US"},
		SourceTexts:     []string{"hello"},
		TargetLanguages: []string{"es"},
		TargetTexts:     []string{"hola"},
		Metadata: map[string]any{
			"provider": "test",
		},
	}

	if len(data.Words) != 1 || data.Words[0].Text != "hello" {
		t.Fatalf("Words = %#v, want timed word", data.Words)
	}
	if data.Words[0].StartTime != 0.1 || data.Words[0].EndTime != 0.4 {
		t.Fatalf("word timing = (%v, %v), want (0.1, 0.4)", data.Words[0].StartTime, data.Words[0].EndTime)
	}
	if data.SourceLanguages[0] != "en-US" || data.TargetLanguages[0] != "es" {
		t.Fatalf("language metadata = %#v/%#v, want source and target language slices", data.SourceLanguages, data.TargetLanguages)
	}
	if data.SourceTexts[0] != "hello" || data.TargetTexts[0] != "hola" {
		t.Fatalf("translation text metadata = %#v/%#v, want source and target text slices", data.SourceTexts, data.TargetTexts)
	}
	if data.Metadata["provider"] != "test" {
		t.Fatalf("Metadata[provider] = %v, want test", data.Metadata["provider"])
	}
}

func TestSpeechEventCarriesReferenceUsageAndSpeechStartTime(t *testing.T) {
	usage := &RecognitionUsage{
		AudioDuration: 1.25,
		InputTokens:   3,
		OutputTokens:  5,
	}
	startTime := 42.5
	event := SpeechEvent{
		Type:             SpeechEventRecognitionUsage,
		RequestID:        "req-1",
		RecognitionUsage: usage,
		SpeechStartTime:  &startTime,
	}

	if event.RecognitionUsage == nil {
		t.Fatal("RecognitionUsage = nil, want structured usage data")
	}
	if event.RecognitionUsage.AudioDuration != 1.25 {
		t.Fatalf("AudioDuration = %v, want 1.25", event.RecognitionUsage.AudioDuration)
	}
	if event.RecognitionUsage.InputTokens != 3 || event.RecognitionUsage.OutputTokens != 5 {
		t.Fatalf("tokens = (%d, %d), want (3, 5)", event.RecognitionUsage.InputTokens, event.RecognitionUsage.OutputTokens)
	}
	if event.SpeechStartTime == nil || *event.SpeechStartTime != 42.5 {
		t.Fatalf("SpeechStartTime = %v, want 42.5", event.SpeechStartTime)
	}
}
