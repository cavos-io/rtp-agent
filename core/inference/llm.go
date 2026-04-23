package inference

import (
	"context"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

var reasoningUnsupportedParams = map[string]bool{
	"temperature":       true,
	"top_p":             true,
	"presence_penalty":  true,
	"frequency_penalty": true,
	"logit_bias":        true,
	"logprobs":          true,
	"top_logprobs":      true,
	"n":                 true,
}

var unsupportedParamsByPrefix = map[string]map[string]bool{
	"o1":    reasoningUnsupportedParams,
	"o3":    reasoningUnsupportedParams,
	"o4":    reasoningUnsupportedParams,
	"gpt-5": reasoningUnsupportedParams,
}

func dropUnsupportedParams(model string, params map[string]any) map[string]any {
	modelName := model
	if lastSlash := strings.LastIndex(model, "/"); lastSlash != -1 {
		modelName = model[lastSlash+1:]
	}

	for prefix, unsupported := range unsupportedParamsByPrefix {
		if strings.HasPrefix(modelName, prefix) {
			newParams := make(map[string]any)
			for k, v := range params {
				if !unsupported[k] {
					newParams[k] = v
				}
			}
			return newParams
		}
	}
	return params
}

type LLM struct {
	model     string
	apiKey    string
	apiSecret string
	baseURL   string
}

type LLMOption func(*LLM)

func WithLLMBaseURL(url string) LLMOption {
	return func(l *LLM) {
		l.baseURL = url
	}
}

func NewLLM(model string, apiKey, apiSecret string, opts ...LLMOption) *LLM {
	l := &LLM{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   "https://agent-gateway.livekit.cloud/v1",
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	token, err := CreateAccessToken(l.apiKey, l.apiSecret, time.Hour)
	if err != nil {
		return nil, err
	}

	// For parity, we should apply parameter filtering here if LLM interface supported extra params
	// but currently it is quite minimal.
	
	inner := openai.NewOpenAILLMWithBaseURL(token, l.model, l.baseURL)
	return inner.Chat(ctx, chatCtx, opts...)
}

