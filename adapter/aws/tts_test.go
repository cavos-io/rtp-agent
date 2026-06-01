package aws

import (
	"bytes"
	"io"
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
