package fireworksai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type FireworksLLM struct {
	inner *openai.OpenAILLM
	err   error
}

func NewFireworksLLM(apiKey string, model string) *FireworksLLM {
	inner, err := openai.NewFireworksOpenAILLM(model, apiKey)
	return &FireworksLLM{
		inner: inner,
		err:   err,
	}
}

func (l *FireworksLLM) Model() string {
	if l.inner == nil {
		return ""
	}
	return l.inner.Model()
}
func (l *FireworksLLM) Provider() string {
	return "fireworks"
}

func (l *FireworksLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
