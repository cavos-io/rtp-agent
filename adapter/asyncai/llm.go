package asyncai

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type AsyncAILLM struct {
	inner *openai.OpenAILLM
}

func NewAsyncAILLM(apiKey string, model string) *AsyncAILLM {
	if model == "" {
		model = "asyncai-default"
	}
	return &AsyncAILLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.async.ai/v1"),
	}
}

func (l *AsyncAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

