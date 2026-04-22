package mistralai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type MistralLLM struct {
	inner *openai.OpenAILLM
}

func NewMistralLLM(apiKey string, model string, opts ...openai.Option) *MistralLLM {
	if model == "" {
		model = "mistral-large-latest"
	}
	defaultOpts := []openai.Option{openai.WithBaseURL("https://api.mistral.ai/v1")}
	finalOpts := append(defaultOpts, opts...)
	return &MistralLLM{
		inner: openai.NewOpenAILLM(apiKey, model, finalOpts...),
	}
}

func (l *MistralLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

