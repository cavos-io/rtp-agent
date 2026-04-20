package fireworksai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type FireworksLLM struct {
	inner *openai.OpenAILLM
}

func NewFireworksLLM(apiKey string, model string) *FireworksLLM {
	if model == "" {
		model = "accounts/fireworks/models/firefunction-v1"
	}
	return &FireworksLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.fireworks.ai/inference/v1"),
	}
}

func (l *FireworksLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

