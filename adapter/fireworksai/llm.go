package fireworksai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type FireworksLLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewFireworksLLM(apiKey string, model string, opts ...Option) *FireworksLLM {
	if model == "" {
		model = "accounts/fireworks/models/firefunction-v1"
	}
	
	// Default base URL for Fireworks
	defaultOpts := []Option{WithBaseURL("https://api.fireworks.ai/inference/v1")}
	defaultOpts = append(defaultOpts, opts...)
	
	return &FireworksLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *FireworksLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
