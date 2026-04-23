package baseten

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type BasetenLLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewBasetenLLM(apiKey string, model string, opts ...Option) *BasetenLLM {
	if model == "" {
		model = "baseten-llama-3"
	}
	defaultOpts := []Option{WithBaseURL("https://bridge.baseten.co/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &BasetenLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *BasetenLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

