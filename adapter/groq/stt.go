package groq

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
)

const (
	defaultGroqSTTBaseURL      = "https://api.groq.com/openai/v1"
	defaultGroqSTTModel        = "whisper-large-v3-turbo"
	defaultGroqSTTLanguage     = "en"
	defaultGroqSTTTotalTimeout = 30 * time.Second
)

type STT struct {
	inner          *openai.STT
	apiKey         string
	baseURL        string
	model          string
	language       string
	detectLanguage bool
	prompt         string
	httpClient     interface {
		Do(*http.Request) (*http.Response, error)
	}
}

type STTOption func(*STT)

func WithGroqSTTBaseURL(baseURL string) STTOption {
	return func(s *STT) {
		if baseURL != "" {
			s.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqSTTLanguage(language string) STTOption {
	return func(s *STT) {
		s.language = language
	}
}

func WithGroqSTTDetectLanguage(detectLanguage bool) STTOption {
	return func(s *STT) {
		s.detectLanguage = detectLanguage
		if detectLanguage {
			s.language = ""
		}
	}
}

func WithGroqSTTPrompt(prompt string) STTOption {
	return func(s *STT) {
		s.prompt = prompt
	}
}

func withGroqSTTHTTPClient(client interface {
	Do(*http.Request) (*http.Response, error)
}) STTOption {
	return func(s *STT) {
		s.httpClient = client
	}
}

func NewSTT(apiKey string, model string, opts ...STTOption) (*STT, error) {
	apiKey = resolveGroqAPIKey(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("GROQ_API_KEY is required, either as argument or set GROQ_API_KEY environmental variable")
	}
	if model == "" {
		model = defaultGroqSTTModel
	}
	provider := &STT{
		apiKey:   apiKey,
		baseURL:  defaultGroqSTTBaseURL,
		model:    model,
		language: defaultGroqSTTLanguage,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.detectLanguage {
		provider.language = ""
	}

	openAIOpts := []openai.STTOption{
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
	if provider.httpClient != nil {
		openAIOpts = append(openAIOpts, openai.WithOpenAISTTHTTPClient(provider.httpClient))
	}
	inner, err := openai.NewSTT(provider.apiKey, provider.model, openAIOpts...)
	if err != nil {
		return nil, err
	}
	provider.inner = inner
	return provider, nil
}

func (s *STT) Label() string { return "groq.STT" }
func (s *STT) Provider() string {
	return groqProviderHost(s.baseURL, "groq")
}
func (s *STT) Model() string { return s.model }
func (s *STT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{OfflineRecognize: true}
}

func (s *STT) Stream(ctx context.Context, language string) (stt.RecognizeStream, error) {
	return s.inner.Stream(ctx, language)
}

func (s *STT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*stt.SpeechEvent, error) {
	reqCtx, cancel := context.WithTimeout(ctx, defaultGroqSTTTotalTimeout)
	defer cancel()
	return s.inner.Recognize(reqCtx, frames, language)
}

func (s *STT) Close() error {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

// Deprecated: use STT.
type GroqSTT = STT

// Deprecated: use STTOption.
type GroqSTTOption = STTOption

// Deprecated: use NewSTT.
func NewGroqSTT(apiKey string, model string, opts ...STTOption) (*STT, error) {
	return NewSTT(apiKey, model, opts...)
}
