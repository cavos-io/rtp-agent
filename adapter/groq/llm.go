package groq

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type GroqLLM struct {
	inner  *openai.OpenAILLM
	apiKey string
}

func NewGroqLLM(apiKey string, model string) *GroqLLM {
	resolvedAPIKey := resolveGroqAPIKey(apiKey)
	return &GroqLLM{
		inner:  openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, "https://api.groq.com/openai/v1"),
		apiKey: resolvedAPIKey,
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
	if l.apiKey == "" {
		return nil, fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
