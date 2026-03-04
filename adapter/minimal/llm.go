package minimal

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type MinimalLLM struct {
	inner *openai.OpenAILLM
}

func NewMinimalLLM(apiKey string, model string) *MinimalLLM {
	if model == "" {
		model = "minimal-default"
	}
	return &MinimalLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.minimal.ai/v1"),
	}
}

func (l *MinimalLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
