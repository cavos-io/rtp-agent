package telnyx

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultTelnyxLLMModel = "meta-llama/Meta-Llama-3.1-70B-Instruct"
	defaultTelnyxLLMURL   = "https://api.telnyx.com/v2/ai"
)

type TelnyxLLM struct {
	inner   *openai.OpenAILLM
	baseURL string
}

func NewTelnyxLLM(apiKey string, model string) *TelnyxLLM {
	if model == "" {
		model = defaultTelnyxLLMModel
	}
	return &TelnyxLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(resolveTelnyxLLMAPIKey(apiKey), model, defaultTelnyxLLMURL),
		baseURL: defaultTelnyxLLMURL,
	}
}

func resolveTelnyxLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("TELNYX_API_KEY")
}

func (l *TelnyxLLM) Model() string {
	return l.inner.Model()
}

func (l *TelnyxLLM) BaseURL() string {
	return l.baseURL
}

func (l *TelnyxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
