package groq

import (
	"context"

	openaiAdapter "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/model"
)

const groqBaseURL = "https://api.groq.com/openai/v1"

// GroqSTT wraps OpenAISTT pointed at the Groq Whisper API endpoint.
// Groq's Whisper API is OpenAI-compatible; keyword boosting is handled via
// the 'prompt' parameter since Groq has no native keyword-boost feature.
type GroqSTT struct {
	inner *openaiAdapter.OpenAISTT
}

// NewGroqSTT creates a Groq Whisper STT provider.
//   - apiKey: GROQ_API_KEY
//   - model:  Groq model name (e.g. "whisper-large-v3"); empty = "whisper-1"
//   - language: ISO-639-1 code (e.g. "en", "id"); empty = auto-detect
//   - prompt: optional biasing prompt (keyword hints, conversation context)
func NewGroqSTT(apiKey, model, language, prompt string) *GroqSTT {
	inner := openaiAdapter.NewOpenAISTTWithPrompt(apiKey, model, groqBaseURL, prompt)
	return &GroqSTT{inner: inner}
}

func (g *GroqSTT) Label() string { return "groq.STT" }

func (g *GroqSTT) Capabilities() stt.STTCapabilities {
	return g.inner.Capabilities()
}

func (g *GroqSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return g.inner.Stream(ctx, language)
}

func (g *GroqSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return g.inner.Recognize(ctx, frames, language)
}
