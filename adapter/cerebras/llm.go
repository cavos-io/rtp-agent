package cerebras

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

const (
	defaultCerebrasModel   = "llama3.1-8b"
	defaultCerebrasBaseURL = "https://api.cerebras.ai/v1"
)

type CerebrasLLM struct {
	inner   *openai.OpenAILLM
	baseURL string
}

func NewCerebrasLLM(apiKey string, model string) *CerebrasLLM {
	if model == "" {
		model = defaultCerebrasModel
	}
	return &CerebrasLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(apiKey, model, defaultCerebrasBaseURL),
		baseURL: defaultCerebrasBaseURL,
	}
}

func (l *CerebrasLLM) Model() string {
	return l.inner.Model()
}

func (l *CerebrasLLM) BaseURL() string {
	return l.baseURL
}

func (l *CerebrasLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
