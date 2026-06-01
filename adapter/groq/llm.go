package groq

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GroqLLM struct {
	inner *openai.OpenAILLM
}

func NewGroqLLM(apiKey string, model string) *GroqLLM {
	return &GroqLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.groq.com/openai/v1"),
	}
}

func (l *GroqLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
