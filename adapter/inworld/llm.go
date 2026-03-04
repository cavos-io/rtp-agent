package inworld

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type InworldLLM struct {
	inner *openai.OpenAILLM
}

func NewInworldLLM(apiKey string, model string) *InworldLLM {
	if model == "" {
		model = "inworld-default"
	}
	return &InworldLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.inworld.ai/v1"),
	}
}

func (l *InworldLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
