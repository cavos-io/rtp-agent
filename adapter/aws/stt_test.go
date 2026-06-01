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
