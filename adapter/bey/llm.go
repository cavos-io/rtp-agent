package bey

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type BeyLLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewBeyLLM(apiKey string, model string, opts ...Option) *BeyLLM {
	if model == "" {
		model = "bey-default"
	}
	defaultOpts := []Option{WithBaseURL("https://api.bey.com/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &BeyLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *BeyLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

