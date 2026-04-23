package aws

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/polly"
)

type mockPollyClient struct {
	synthesizeSpeechFunc func(ctx context.Context, params *polly.SynthesizeSpeechInput, optFns ...func(*polly.Options)) (*polly.SynthesizeSpeechOutput, error)
}

func (m *mockPollyClient) SynthesizeSpeech(ctx context.Context, params *polly.SynthesizeSpeechInput, optFns ...func(*polly.Options)) (*polly.SynthesizeSpeechOutput, error) {
	return m.synthesizeSpeechFunc(ctx, params, optFns...)
}

func TestAWSTTS_Synthesize(t *testing.T) {
	mockClient := &mockPollyClient{
		synthesizeSpeechFunc: func(ctx context.Context, params *polly.SynthesizeSpeechInput, optFns ...func(*polly.Options)) (*polly.SynthesizeSpeechOutput, error) {
			return &polly.SynthesizeSpeechOutput{
				AudioStream: io.NopCloser(bytes.NewReader(make([]byte, 1024))),
			}, nil
		},
	}

	tts, err := NewAWSTTS(context.Background(), "us-east-1", "Matthew", WithPollyClient(mockClient))
	if err != nil {
		t.Fatalf("NewAWSTTS failed: %v", err)
	}

	stream, err := tts.Synthesize(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if len(audio.Frame.Data) != 1024 {
		t.Errorf("Expected 1024 bytes, got %d", len(audio.Frame.Data))
	}
}
