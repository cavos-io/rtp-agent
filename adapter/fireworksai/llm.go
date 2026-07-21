package fireworksai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type LLM struct {
	inner *openai.LLM
	err   error
}

func NewLLM(apiKey string, model string) *LLM {
	inner, err := openai.NewFireworksOpenAILLM(model, apiKey)
	return &LLM{
		inner: inner,
		err:   err,
	}
}

func (l *LLM) Model() string {
	if l.inner == nil {
		return ""
	}
	return l.inner.Model()
}
func (l *LLM) Provider() string {
	return "fireworks"
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}

// Deprecated: use LLM.
type FireworksLLM = LLM

// Deprecated: use NewLLM.
func NewFireworksLLM(apiKey string, model string) *LLM {
	return NewLLM(apiKey, model)
}
