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
