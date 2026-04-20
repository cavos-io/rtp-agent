package mistralai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type MistralLLM struct {
	inner *openai.OpenAILLM
}

func NewMistralLLM(apiKey string, model string) *MistralLLM {
	if model == "" {
		model = "mistral-large-latest"
	}
	return &MistralLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.mistral.ai/v1"),
	}
}

func (l *MistralLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

