package upliftai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type UpliftAILLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewUpliftAILLM(apiKey string, model string, opts ...Option) *UpliftAILLM {
	if model == "" {
		model = "upliftai-default"
	}
	defaultOpts := []Option{WithBaseURL("https://api.uplift.ai/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &UpliftAILLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *UpliftAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
