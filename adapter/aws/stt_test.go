package aws

import (
	"testing"

	awsconfig "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribestreaming/types"
)

func TestAWSSpeechDataFromAlternativePreservesPronunciationItems(t *testing.T) {
	alt := types.Alternative{
		Transcript: awsconfig.String("hello world"),
		Items: []types.Item{
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("hello"),
				StartTime:  0.1,
				EndTime:    0.3,
				Confidence: awsconfig.Float64(0.94),
				Speaker:    awsconfig.String("spk_0"),
			},
			{
				Type:    types.ItemTypePunctuation,
				Content: awsconfig.String(","),
			},
			{
				Type:       types.ItemTypePronunciation,
				Content:    awsconfig.String("world"),
				StartTime:  0.4,
				EndTime:    0.8,
				Confidence: awsconfig.Float64(0.91),
				Speaker:    awsconfig.String("spk_1"),
			},
		},
	}

	data := awsSpeechDataFromAlternative(alt)
	if data.Text != "hello world" {
		t.Fatalf("text = %q, want hello world", data.Text)
	}
	if data.Confidence != 1.0 {
		t.Fatalf("confidence = %v, want 1", data.Confidence)
	}
	if len(data.Words) != 2 {
		t.Fatalf("words = %d, want 2", len(data.Words))
	}
	if got := data.Words[0]; got.Text != "hello" || got.StartTime != 0.1 || got.EndTime != 0.3 || got.Confidence != 0.94 || got.SpeakerID != "spk_0" {
		t.Fatalf("first word = %+v, want hello timing with speaker", got)
	}
	if got := data.Words[1]; got.Text != "world" || got.StartTime != 0.4 || got.EndTime != 0.8 || got.Confidence != 0.91 || got.SpeakerID != "spk_1" {
		t.Fatalf("second word = %+v, want world timing with speaker", got)
	}
}

func TestAWSSTTCapabilitiesAdvertiseWordAlignment(t *testing.T) {
	provider := &AWSSTT{}

	if got := provider.Capabilities().AlignedTranscript; got != "word" {
		t.Fatalf("AlignedTranscript = %q, want word", got)
	}
}

func TestAWSSTTStreamInputDefaultsMatchReference(t *testing.T) {
	provider := newAWSSTTWithClient(nil)

	input := buildAWSStartStreamTranscriptionInput(provider, "")

	if input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language = %q, want en-US", input.LanguageCode)
	}
	if input.MediaEncoding != types.MediaEncodingPcm {
		t.Fatalf("media encoding = %q, want pcm", input.MediaEncoding)
	}
	if input.MediaSampleRateHertz == nil || *input.MediaSampleRateHertz != 24000 {
		t.Fatalf("sample rate = %v, want 24000", input.MediaSampleRateHertz)
	}
}

func TestAWSSTTStreamInputUsesProviderOptions(t *testing.T) {
	provider := newAWSSTTWithClient(nil,
		WithAWSSTTSampleRate(8000),
		WithAWSSTTVocabularyName("support_terms"),
		WithAWSSTTShowSpeakerLabel(true),
		WithAWSSTTEnablePartialResultsStabilization(true),
		WithAWSSTTPartialResultsStability(types.PartialResultsStabilityHigh),
		WithAWSSTTLanguageModelName("support-model"),
	)

	input := buildAWSStartStreamTranscriptionInput(provider, "id-ID")

	if input.LanguageCode != types.LanguageCodeIdId {
		t.Fatalf("language = %q, want id-ID", input.LanguageCode)
	}
	if input.MediaSampleRateHertz == nil || *input.MediaSampleRateHertz != 8000 {
		t.Fatalf("sample rate = %v, want 8000", input.MediaSampleRateHertz)
	}
	if input.VocabularyName == nil || *input.VocabularyName != "support_terms" {
		t.Fatalf("vocabulary name = %v, want support_terms", input.VocabularyName)
	}
	if !input.ShowSpeakerLabel {
		t.Fatal("show speaker label = false, want true")
	}
	if !input.EnablePartialResultsStabilization {
		t.Fatal("partial stabilization = false, want true")
	}
	if input.PartialResultsStability != types.PartialResultsStabilityHigh {
		t.Fatalf("partial stability = %q, want high", input.PartialResultsStability)
	}
	if input.LanguageModelName == nil || *input.LanguageModelName != "support-model" {
		t.Fatalf("language model = %v, want support-model", input.LanguageModelName)
	}
}

func TestAWSSTTStreamInputOmitsLanguageWhenIdentifyingLanguage(t *testing.T) {
	provider := newAWSSTTWithClient(nil,
		WithAWSSTTIdentifyLanguage(true),
		WithAWSSTTLanguageOptions("en-US,id-ID"),
		WithAWSSTTPreferredLanguage(types.LanguageCodeIdId),
	)

	input := buildAWSStartStreamTranscriptionInput(provider, "en-US")

	if input.LanguageCode != "" {
		t.Fatalf("language = %q, want empty when identifying language", input.LanguageCode)
	}
	if !input.IdentifyLanguage {
		t.Fatal("identify language = false, want true")
	}
	if input.LanguageOptions == nil || *input.LanguageOptions != "en-US,id-ID" {
		t.Fatalf("language options = %v, want en-US,id-ID", input.LanguageOptions)
	}
	if input.PreferredLanguage != types.LanguageCodeIdId {
		t.Fatalf("preferred language = %q, want id-ID", input.PreferredLanguage)
	}
}
