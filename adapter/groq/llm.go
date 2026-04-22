package groq

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GroqLLM struct {
	inner *openai.OpenAILLM
}

func NewGroqLLM(apiKey string, model string, opts ...openai.Option) *GroqLLM {
	defaultOpts := []openai.Option{openai.WithBaseURL("https://api.groq.com/openai/v1")}
	finalOpts := append(defaultOpts, opts...)
	return &GroqLLM{
		inner: openai.NewOpenAILLM(apiKey, model, finalOpts...),
	}
}

func (l *GroqLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

