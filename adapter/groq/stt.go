package groq

import (
	"context"
	"fmt"
	"strings"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultGroqSTTBaseURL  = "https://api.groq.com/openai/v1"
	defaultGroqSTTModel    = "whisper-large-v3-turbo"
	defaultGroqSTTLanguage = "en"
)

type GroqSTT struct {
	inner          *openai.OpenAISTT
	apiKey         string
	baseURL        string
	model          string
	language       string
	detectLanguage bool
	prompt         string
}

type GroqSTTOption func(*GroqSTT)

func WithGroqSTTBaseURL(baseURL string) GroqSTTOption {
	return func(s *GroqSTT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqSTTLanguage(language string) GroqSTTOption {
	return func(s *GroqSTT) {
		s.language = language
	}
}

func WithGroqSTTDetectLanguage(detectLanguage bool) GroqSTTOption {
	return func(s *GroqSTT) {
		s.detectLanguage = detectLanguage
		if detectLanguage {
			s.language = ""
		}
	}
}

func WithGroqSTTPrompt(prompt string) GroqSTTOption {
	return func(s *GroqSTT) {
		s.prompt = prompt
	}
}

func NewGroqSTT(apiKey string, model string, opts ...GroqSTTOption) (*GroqSTT, error) {
	apiKey = resolveGroqAPIKey(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY is required, either as argument or set GROQ_API_KEY environmental variable")
	}
	if model == "" {
		model = defaultGroqSTTModel
	}
	provider := &GroqSTT{
		apiKey:   apiKey,
		baseURL:  defaultGroqSTTBaseURL,
		model:    model,
		language: defaultGroqSTTLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}

	openAIOpts := []openai.OpenAISTTOption{
		openai.WithOpenAISTTBaseURL(provider.baseURL),
		openai.WithOpenAISTTRealtime(false),
	}
	if provider.detectLanguage {
		openAIOpts = append(openAIOpts, openai.WithOpenAISTTDetectLanguage(true))
	} else {
		openAIOpts = append(openAIOpts, openai.WithOpenAISTTLanguage(provider.language))
	}
	if provider.prompt != "" {
		openAIOpts = append(openAIOpts, openai.WithOpenAISTTPrompt(provider.prompt))
	}
	inner, err := openai.NewOpenAISTT(provider.apiKey, provider.model, openAIOpts...)
	if err != nil {
		return nil, err
	}
	provider.inner = inner
	return provider, nil
}

func (s *GroqSTT) Label() string { return "groq.STT" }
func (s *GroqSTT) Provider() string {
	return "groq"
}
func (s *GroqSTT) Model() string { return s.model }
func (s *GroqSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{OfflineRecognize: true}
}

func (s *GroqSTT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return s.inner.Stream(ctx, language)
}

func (s *GroqSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	return s.inner.Recognize(ctx, frames, language)
}
