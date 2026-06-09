package fireworksai

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const defaultFireworksLLMModel = "accounts/fireworks/models/llama-v3p3-70b-instruct"

type FireworksLLM struct {
	inner  *openai.OpenAILLM
	apiKey string
}

func NewFireworksLLM(apiKey string, model string) *FireworksLLM {
	if model == "" {
		model = defaultFireworksLLMModel
	}
	resolvedAPIKey := resolveFireworksLLMAPIKey(apiKey)
	return &FireworksLLM{
		inner:  openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, "https://api.fireworks.ai/inference/v1"),
		apiKey: resolvedAPIKey,
	}
}

func resolveFireworksLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("FIREWORKS_API_KEY")
}

func (l *FireworksLLM) Model() string { return l.inner.Model() }
func (l *FireworksLLM) Provider() string {
	return "fireworks"
}

func (l *FireworksLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("fireworks API key is required, either as argument or set FIREWORKS_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
