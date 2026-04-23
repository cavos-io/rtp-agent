package nvidia

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type NvidiaLLM struct {
	inner *openai.OpenAILLM
}

type Option = openai.Option

func WithBaseURL(url string) Option {
	return openai.WithBaseURL(url)
}

func NewNvidiaLLM(apiKey string, model string, opts ...Option) *NvidiaLLM {
	if model == "" {
		model = "meta/llama3-70b-instruct"
	}
	defaultOpts := []Option{WithBaseURL("https://integrate.api.nvidia.com/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &NvidiaLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *NvidiaLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

