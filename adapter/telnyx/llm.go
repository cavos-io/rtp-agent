package telnyx

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type TelnyxLLM struct {
	inner *openai.OpenAILLM
}

func NewTelnyxLLM(apiKey string, model string) *TelnyxLLM {
	if model == "" {
		model = "telnyx-default"
	}
	return &TelnyxLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.telnyx.com/v2/ai/v1"),
	}
}

func (l *TelnyxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
