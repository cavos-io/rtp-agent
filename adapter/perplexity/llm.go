package perplexity

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultPerplexityModel   = "sonar-pro"
	defaultPerplexityBaseURL = "https://api.perplexity.ai"
)

type PerplexityLLM struct {
	inner   *openai.OpenAILLM
	baseURL string
}

func NewPerplexityLLM(apiKey string, model string) *PerplexityLLM {
	if model == "" {
		model = defaultPerplexityModel
	}
	return &PerplexityLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(resolvePerplexityAPIKey(apiKey), model, defaultPerplexityBaseURL),
		baseURL: defaultPerplexityBaseURL,
	}
}

func resolvePerplexityAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("PERPLEXITY_API_KEY")
}

func (l *PerplexityLLM) Model() string {
	return l.inner.Model()
}

func (l *PerplexityLLM) BaseURL() string {
	return l.baseURL
}

func (l *PerplexityLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
