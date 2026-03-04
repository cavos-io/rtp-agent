package nvidia

import (
	"context"

	"github.com/cavos-io/conversation-worker/adapter/openai"
	"github.com/cavos-io/conversation-worker/core/llm"
)

type NvidiaLLM struct {
	inner *openai.OpenAILLM
}

func NewNvidiaLLM(apiKey string, model string) *NvidiaLLM {
	if model == "" {
		model = "meta/llama3-70b-instruct"
	}
	return &NvidiaLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://integrate.api.nvidia.com/v1"),
	}
}

func (l *NvidiaLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
