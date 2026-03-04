package trugen

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type TrugenLLM struct {
	inner *openai.OpenAILLM
}

func NewTrugenLLM(apiKey string, model string) *TrugenLLM {
	if model == "" {
		model = "trugen-default"
	}
	return &TrugenLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.trugen.ai/v1"),
	}
}

func (l *TrugenLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
