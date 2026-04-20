package hedra

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type HedraLLM struct {
	inner *openai.OpenAILLM
}

func NewHedraLLM(apiKey string, model string) *HedraLLM {
	if model == "" {
		model = "hedra-default"
	}
	return &HedraLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.hedra.com/v1"),
	}
}

func (l *HedraLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

