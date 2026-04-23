package cambai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type CambaiLLM struct {
	inner *openai.OpenAILLM
}

type LLMOption = openai.Option

func WithLLMBaseURL(url string) LLMOption {
	return openai.WithBaseURL(url)
}

func NewCambaiLLM(apiKey string, model string, opts ...LLMOption) *CambaiLLM {
	if model == "" {
		model = "cambai-default"
	}
	defaultOpts := []LLMOption{WithLLMBaseURL("https://api.cambai.com/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &CambaiLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *CambaiLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

