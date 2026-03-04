package simli

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type SimliLLM struct {
	inner *openai.OpenAILLM
}

func NewSimliLLM(apiKey string, model string) *SimliLLM {
	if model == "" {
		model = "simli-default"
	}
	return &SimliLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.simli.ai/v1"),
	}
}

func (l *SimliLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
