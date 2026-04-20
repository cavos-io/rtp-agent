package simli

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
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

