package aws

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/polly/types"
)

func TestAWSTTSDefaultsMatchReference(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	if provider.voice != types.VoiceIdRuth {
		t.Fatalf("voice = %q, want Ruth", provider.voice)
	}
	if provider.engine != types.EngineGenerative {
		t.Fatalf("engine = %q, want generative", provider.engine)
	}
	if provider.outputFormat != types.OutputFormatMp3 {
		t.Fatalf("output format = %q, want mp3", provider.outputFormat)
	}
	if provider.textType != types.TextTypeText {
		t.Fatalf("text type = %q, want text", provider.textType)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}
	if provider.Label() != "aws.TTS" {
		t.Fatalf("Label = %q, want aws.TTS", provider.Label())
	}
	if provider.Model() != "generative" {
		t.Fatalf("Model = %q, want generative", provider.Model())
	}
	if provider.Provider() != "Amazon Polly" {
		t.Fatalf("Provider = %q, want Amazon Polly", provider.Provider())
	}
	if provider.SampleRate() != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", provider.SampleRate())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false for Polly synthesize")
	}
}

func TestNewAWSTTSUsesReferenceDefaults(t *testing.T) {
	provider, err := NewAWSTTS(context.Background(), "", "")
	if err != nil {
		t.Fatalf("NewAWSTTS error = %v, want nil with SDK default config", err)
	}
	if provider.voice != types.VoiceIdRuth {
		t.Fatalf("voice = %q, want Ruth", provider.voice)
	}
	if provider.Model() != "generative" {
		t.Fatalf("Model = %q, want generative", provider.Model())
	}
}

func TestAWSTTSSynthesizeInputUsesProviderOptions(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "Matthew",
		WithAWSTTSEngine(types.EngineNeural),
		WithAWSTTSTextType(types.TextTypeSsml),
		WithAWSTTSLanguage(types.LanguageCodeEnUs),
		WithAWSTTSSampleRate(24000),
	)

	input := buildAWSSynthesizeSpeechInput(provider, "<speak>Hello</speak>")

	if input.Text == nil || *input.Text != "<speak>Hello</speak>" {
		t.Fatalf("text = %v, want SSML input", input.Text)
	}
	if input.VoiceId != types.VoiceIdMatthew {
		t.Fatalf("voice = %q, want Matthew", input.VoiceId)
	}
	if input.Engine != types.EngineNeural {
		t.Fatalf("engine = %q, want neural", input.Engine)
	}
	if input.OutputFormat != types.OutputFormatMp3 {
		t.Fatalf("output format = %q, want mp3", input.OutputFormat)
	}
	if input.TextType != types.TextTypeSsml {
		t.Fatalf("text type = %q, want ssml", input.TextType)
	}
	if input.LanguageCode != types.LanguageCodeEnUs {
		t.Fatalf("language = %q, want en-US", input.LanguageCode)
	}
	if input.SampleRate == nil || *input.SampleRate != "24000" {
		t.Fatalf("sample rate = %v, want 24000", input.SampleRate)
	}
}

func TestAWSTTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	provider.UpdateOptions(
		WithAWSTTSVoice(types.VoiceIdJoanna),
		WithAWSTTSEngine(types.EngineStandard),
		WithAWSTTSTextType(types.TextTypeSsml),
	)

	if provider.voice != types.VoiceIdJoanna {
		t.Fatalf("voice = %q, want Joanna", provider.voice)
	}
	if provider.engine != types.EngineStandard {
		t.Fatalf("engine = %q, want standard", provider.engine)
	}
	if provider.textType != types.TextTypeSsml {
		t.Fatalf("text type = %q, want ssml", provider.textType)
	}
}

func TestAWSTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream:     io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func TestAWSTTSSynthesizeRequiresConfiguredClient(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	_, err := provider.Synthesize(context.Background(), "hello")

	if err == nil || !strings.Contains(err.Error(), "client is not configured") {
		t.Fatalf("Synthesize error = %v, want configured-client error", err)
	}
}

func TestAWSTTSStreamReportsUnsupported(t *testing.T) {
	provider := newAWSTTSWithClient(nil, "")

	_, err := provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Stream error = %v, want unsupported error", err)
	}
}

func TestAWSTTSChunkedStreamEOFAndClose(t *testing.T) {
	stream := &awsTTSChunkedStream{
		stream:     io.NopCloser(bytes.NewReader(nil)),
		sampleRate: 16000,
	}

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next err = %v, want EOF", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close err = %v, want nil", err)
	}

	empty := &awsTTSChunkedStream{}
	if err := empty.Close(); err != nil {
		t.Fatalf("empty Close err = %v, want nil", err)
	}
}
