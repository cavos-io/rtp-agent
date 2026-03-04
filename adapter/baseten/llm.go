package baseten

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type BasetenLLM struct {
	inner *openai.OpenAILLM
}

func NewBasetenLLM(apiKey string, model string) *BasetenLLM {
	if model == "" {
		model = "baseten-llama-3"
	}
	return &BasetenLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://bridge.baseten.co/v1"),
	}
}

func (l *BasetenLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
