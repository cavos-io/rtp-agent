package cerebras

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultCerebrasModel   = "llama3.1-8b"
	defaultCerebrasBaseURL = "https://api.cerebras.ai/v1"
)

type CerebrasLLM struct {
	inner   *openai.OpenAILLM
	apiKey  string
	baseURL string
}

func NewCerebrasLLM(apiKey string, model string) *CerebrasLLM {
	if model == "" {
		model = defaultCerebrasModel
	}
	resolvedAPIKey := resolveCerebrasAPIKey(apiKey)
	return &CerebrasLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, defaultCerebrasBaseURL),
		apiKey:  resolvedAPIKey,
		baseURL: defaultCerebrasBaseURL,
	}
}

func resolveCerebrasAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("CEREBRAS_API_KEY")
}

func (l *CerebrasLLM) Model() string {
	return l.inner.Model()
}

func (l *CerebrasLLM) Provider() string {
	return "cerebras"
}

func (l *CerebrasLLM) BaseURL() string {
	return l.baseURL
}

func (l *CerebrasLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("cerebras API key is required, either as argument or set CEREBRAS_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
