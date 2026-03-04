package minimax

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type MinimaxLLM struct {
	inner *openai.OpenAILLM
}

func NewMinimaxLLM(apiKey string, model string) *MinimaxLLM {
	if model == "" {
		model = "abab6.5-chat"
	}
	return &MinimaxLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.minimax.chat/v1"),
	}
}

func (l *MinimaxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
