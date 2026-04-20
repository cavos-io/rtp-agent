package telnyx

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
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

