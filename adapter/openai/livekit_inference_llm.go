package openai

import (
	"context"
	"time"

	"github.com/cavos-io/rtp-agent/core/inference"
	"github.com/cavos-io/rtp-agent/core/llm"
)

type LiveKitInferenceLLM struct {
	model     string
	apiKey    string
	apiSecret string
	baseURL   string
}

var _ llm.LLM = (*LiveKitInferenceLLM)(nil)

func NewLiveKitInferenceLLM(model string, apiKey, apiSecret string) *LiveKitInferenceLLM {
	return &LiveKitInferenceLLM{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   "https://agent-gateway.livekit.cloud/v1",
	}
}

func (l *LiveKitInferenceLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	token, err := inference.CreateAccessToken(l.apiKey, l.apiSecret, time.Hour)
	if err != nil {
		return nil, err
	}

	inner := NewOpenAILLMWithBaseURL(token, l.model, l.baseURL)
	return inner.Chat(ctx, chatCtx, opts...)
}
