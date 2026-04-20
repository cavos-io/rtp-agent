package lemonslice

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type LemonSliceLLM struct {
	inner *openai.OpenAILLM
}

func NewLemonSliceLLM(apiKey string, model string) *LemonSliceLLM {
	if model == "" {
		model = "lemonslice-default"
	}
	return &LemonSliceLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.lemonslice.io/v1"),
	}
}

func (l *LemonSliceLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

