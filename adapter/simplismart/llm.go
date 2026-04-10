package simplismart

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type SimplismartLLM struct {
	inner *openai.OpenAILLM
}

func NewSimplismartLLM(apiKey string, model string) *SimplismartLLM {
	if model == "" {
		model = "simplismart-default"
	}
	return &SimplismartLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.simplismart.ai/v1"),
	}
}

func (l *SimplismartLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
