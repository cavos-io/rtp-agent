package upliftai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type UpliftAILLM struct {
	inner *openai.OpenAILLM
}

func NewUpliftAILLM(apiKey string, model string) *UpliftAILLM {
	if model == "" {
		model = "upliftai-default"
	}
	return &UpliftAILLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.uplift.ai/v1"),
	}
}

func (l *UpliftAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
