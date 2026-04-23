package minimax

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type MinimaxLLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewMinimaxLLM(apiKey string, model string, opts ...Option) *MinimaxLLM {
	if model == "" {
		model = "abab6.5-chat"
	}
	defaultOpts := []Option{WithBaseURL("https://api.minimax.chat/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &MinimaxLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *MinimaxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

