package openai

import (
	"testing"

	goopenai "github.com/sashabaranov/go-openai"
)

func TestOpenAITTSDefaultsMatchReference(t *testing.T) {
	provider := NewOpenAITTS("test-key", "", "")

	if provider.model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want %q", provider.model, goopenai.TTSModelGPT4oMini)
	}
	if provider.voice != goopenai.VoiceAsh {
		t.Fatalf("voice = %q, want %q", provider.voice, goopenai.VoiceAsh)
	}
	if provider.responseFormat != goopenai.SpeechResponseFormatMp3 {
		t.Fatalf("responseFormat = %q, want %q", provider.responseFormat, goopenai.SpeechResponseFormatMp3)
	}
	if provider.speed != 1.0 {
		t.Fatalf("speed = %v, want 1.0", provider.speed)
	}
}

func TestOpenAITTSBuildSpeechRequestUsesReferenceOptions(t *testing.T) {
	provider := NewOpenAITTS("test-key", "", "",
		WithOpenAITTSInstructions("speak warmly"),
		WithOpenAITTSSpeed(1.25),
		WithOpenAITTSResponseFormat(goopenai.SpeechResponseFormatPcm),
	)

	got := buildOpenAITTSSpeechRequest(provider, "hello")

	if got.Model != goopenai.TTSModelGPT4oMini {
		t.Fatalf("model = %q, want %q", got.Model, goopenai.TTSModelGPT4oMini)
	}
	if got.Input != "hello" {
		t.Fatalf("input = %q, want hello", got.Input)
	}
	if got.Voice != goopenai.VoiceAsh {
		t.Fatalf("voice = %q, want %q", got.Voice, goopenai.VoiceAsh)
	}
	if got.Instructions != "speak warmly" {
		t.Fatalf("instructions = %q, want speak warmly", got.Instructions)
	}
	if got.ResponseFormat != goopenai.SpeechResponseFormatPcm {
		t.Fatalf("response_format = %q, want %q", got.ResponseFormat, goopenai.SpeechResponseFormatPcm)
	}
	if got.Speed != 1.25 {
		t.Fatalf("speed = %v, want 1.25", got.Speed)
	}
}

func TestOpenAITTSUpdateOptionsMatchesReference(t *testing.T) {
	provider := NewOpenAITTS("test-key", "", "")

	provider.UpdateOptions(
		WithOpenAITTSModel(goopenai.TTSModel1HD),
		WithOpenAITTSVoice(goopenai.VoiceNova),
		WithOpenAITTSSpeed(0.85),
		WithOpenAITTSInstructions("speak softly"),
	)

	if provider.model != goopenai.TTSModel1HD {
		t.Fatalf("model = %q, want %q", provider.model, goopenai.TTSModel1HD)
	}
	if provider.voice != goopenai.VoiceNova {
		t.Fatalf("voice = %q, want %q", provider.voice, goopenai.VoiceNova)
	}
	if provider.speed != 0.85 {
		t.Fatalf("speed = %v, want 0.85", provider.speed)
	}
	if provider.instructions != "speak softly" {
		t.Fatalf("instructions = %q, want speak softly", provider.instructions)
	}
}
