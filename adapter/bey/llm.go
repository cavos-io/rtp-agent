package bey

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type BeyLLM struct {
	inner *openai.OpenAILLM
}

func NewBeyLLM(apiKey string, model string) *BeyLLM {
	if model == "" {
		model = "bey-default"
	}
	return &BeyLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.bey.com/v1"),
	}
}

func (l *BeyLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
