package langchain

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type LLM struct {
	inner *openai.LLM
}

func NewLLM(apiKey string, model string) *LLM {
	if model == "" {
		model = "langchain-default"
	}
	return &LLM{
		inner: openai.NewOpenAILLMWithBaseURL(apiKey, model, "https://api.smith.langchain.com/v1"),
	}
}

func (l *LLM) Model() string { return "unknown" }
func (l *LLM) Provider() string {
	return "LangChain"
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

// Deprecated: use LLM.
type LangchainLLM = LLM

// Deprecated: use NewLLM.
func NewLangchainLLM(apiKey string, model string) *LLM {
	return NewLLM(apiKey, model)
}
