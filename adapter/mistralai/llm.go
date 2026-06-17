package mistralai

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type MistralLLM struct {
	inner  *openai.OpenAILLM
	apiKey string
}

func NewMistralLLM(apiKey string, model string) *MistralLLM {
	if model == "" {
		model = "ministral-8b-latest"
	}
	resolvedAPIKey := resolveMistralLLMAPIKey(apiKey)
	return &MistralLLM{
		inner:  openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, "https://api.mistral.ai/v1"),
		apiKey: resolvedAPIKey,
	}
}

func resolveMistralLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("MISTRAL_API_KEY")
}

func (l *MistralLLM) Model() string {
	return l.inner.Model()
}

func (l *MistralLLM) Provider() string { return "MistralAI" }

func (l *MistralLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("mistral AI API key is required; set MISTRAL_API_KEY or pass api_key")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
