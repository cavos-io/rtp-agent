package telnyx

import (
	"context"
	"fmt"
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
	apiKey  string
	baseURL string
}

func NewTelnyxLLM(apiKey string, model string) *TelnyxLLM {
	if model == "" {
		model = defaultTelnyxLLMModel
	}
	resolvedAPIKey := resolveTelnyxLLMAPIKey(apiKey)
	return &TelnyxLLM{
		inner:   openai.NewOpenAILLMWithBaseURL(resolvedAPIKey, model, defaultTelnyxLLMURL),
		apiKey:  resolvedAPIKey,
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
	if l.apiKey == "" {
		return nil, fmt.Errorf("telnyx AI API key is required, either as argument or set TELNYX_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
