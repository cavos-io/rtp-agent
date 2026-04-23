package telnyx

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type TelnyxLLM struct {
	inner *openai.OpenAILLM
}

type LLMOption = openai.Option

func WithLLMBaseURL(url string) LLMOption {
	return openai.WithBaseURL(url)
}

func NewTelnyxLLM(apiKey string, model string, opts ...LLMOption) *TelnyxLLM {
	if model == "" {
		model = "telnyx-default"
	}
	defaultOpts := []LLMOption{WithLLMBaseURL("https://api.telnyx.com/v2/ai/v1")}
	defaultOpts = append(defaultOpts, opts...)
	return &TelnyxLLM{
		inner: openai.NewOpenAILLM(apiKey, model, defaultOpts...),
	}
}

func (l *TelnyxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

