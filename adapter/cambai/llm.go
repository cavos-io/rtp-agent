package cambai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type CambaiLLM struct {
	inner *openai.OpenAILLM
}

func NewCambaiLLM(apiKey string, model string) *CambaiLLM {
	if model == "" {
		model = "cambai-default"
	}
	return &CambaiLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.cambai.com/v1"),
	}
}

func (l *CambaiLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
