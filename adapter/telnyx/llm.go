package telnyx

import (
	"context"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultTelnyxLLMURL = "https://api.telnyx.com/v2/ai"
)

type TelnyxLLM struct {
	inner   *openai.OpenAILLM
	err     error
	baseURL string
}

func NewTelnyxLLM(apiKey string, model string) *TelnyxLLM {
	inner, err := openai.NewTelnyxOpenAILLM(model, apiKey)
	return &TelnyxLLM{
		inner:   inner,
		err:     err,
		baseURL: defaultTelnyxLLMURL,
	}
}

func (l *TelnyxLLM) Model() string {
	if l.inner == nil {
		return ""
	}
	return l.inner.Model()
}

func (l *TelnyxLLM) BaseURL() string {
	return l.baseURL
}

func (l *TelnyxLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}
