package gradium

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GradiumLLM struct {
	inner *openai.OpenAILLM
}

func NewGradiumLLM(apiKey string, model string) *GradiumLLM {
	if model == "" {
		model = "gradium-default"
	}
	return &GradiumLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.gradium.ai/v1"),
	}
}

func (l *GradiumLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
