package langchain

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type LangchainLLM struct {
	inner *openai.OpenAILLM
}

func NewLangchainLLM(apiKey string, model string) *LangchainLLM {
	if model == "" {
		model = "langchain-default"
	}
	return &LangchainLLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.smith.langchain.com/v1"),
	}
}

func (l *LangchainLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
