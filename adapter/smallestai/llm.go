package smallestai

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type SmallestAILLM struct {
	inner *openai.OpenAILLM
}

func NewSmallestAILLM(apiKey string, model string) *SmallestAILLM {
	if model == "" {
		model = "smallestai-default"
	}
	return &SmallestAILLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.smallest.ai/v1"),
	}
}

func (l *SmallestAILLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
