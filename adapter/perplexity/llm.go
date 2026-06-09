package perplexity

import (
	"context"
	"fmt"
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
	apiKey  string
	baseURL string
}

func NewPerplexityLLM(apiKey string, model string) *PerplexityLLM {
	if model == "" {
		model = defaultPerplexityModel
	}
	resolvedAPIKey := resolvePerplexityAPIKey(apiKey)
	return &PerplexityLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, defaultPerplexityBaseURL),
		apiKey:  resolvedAPIKey,
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
	if l.apiKey == "" {
		return nil, fmt.Errorf("perplexity API key is required, either as argument or set PERPLEXITY_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
