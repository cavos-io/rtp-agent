package groq

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GroqLLM struct {
	inner *openai.OpenAILLM
}

func NewGroqLLM(apiKey string, model string) *GroqLLM {
	return &GroqLLM{
		inner: openai.NewOpenAILLMWithBaseURL(resolveGroqAPIKey(apiKey), model, "https://api.groq.com/openai/v1"),
	}
}

func resolveGroqAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("GROQ_API_KEY")
}

func (l *GroqLLM) Model() string { return l.inner.Model() }
func (l *GroqLLM) Provider() string {
	return "groq"
}

func (l *GroqLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
