package mistralai

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

type MistralLLM struct {
	inner       *openai.OpenAILLM
	apiKey      string
	model       string
	baseURL     string
	httpClient  goopenai.HTTPDoer
	extraParams map[string]any
	toolChoice  llm.ToolChoice
}

type MistralLLMOption func(*MistralLLM)

func WithMistralLLMModel(model string) MistralLLMOption {
	return func(l *MistralLLM) {
		if model != "" {
			l.model = model
		}
	}
}

func WithMistralLLMTemperature(temperature float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("temperature", temperature)
	}
}

func WithMistralLLMTopP(topP float64) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("top_p", topP)
	}
}

func WithMistralLLMMaxCompletionTokens(maxCompletionTokens int) MistralLLMOption {
	return func(l *MistralLLM) {
		l.setExtraParam("max_tokens", maxCompletionTokens)
	}
}

func WithMistralLLMToolChoice(toolChoice llm.ToolChoice) MistralLLMOption {
	return func(l *MistralLLM) {
		l.toolChoice = toolChoice
	}
}

func withMistralLLMHTTPClient(httpClient goopenai.HTTPDoer) MistralLLMOption {
	return func(l *MistralLLM) {
		l.httpClient = httpClient
	}
}

func NewMistralLLM(apiKey string, model string, opts ...MistralLLMOption) *MistralLLM {
	if model == "" {
		model = "ministral-8b-latest"
	}
	resolvedAPIKey := resolveMistralLLMAPIKey(apiKey)
	provider := &MistralLLM{
		apiKey:  resolvedAPIKey,
		model:   model,
		baseURL: "https://api.mistral.ai/v1",
	}
	for _, opt := range opts {
		opt(provider)
	}
	provider.rebuildInner()
	return provider
}

func resolveMistralLLMAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("MISTRAL_API_KEY")
}

func (l *MistralLLM) Model() string {
	return l.model
}

func (l *MistralLLM) Provider() string { return "MistralAI" }

func (l *MistralLLM) UpdateOptions(opts ...MistralLLMOption) {
	for _, opt := range opts {
		opt(l)
	}
	l.rebuildInner()
}

func (l *MistralLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("mistral AI API key is required; set MISTRAL_API_KEY or pass api_key")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}

func (l *MistralLLM) rebuildInner() {
	opts := []openai.OpenAILLMOption{}
	if len(l.extraParams) > 0 {
		opts = append(opts, openai.WithOpenAILLMExtraParams(cloneMistralLLMAnyMap(l.extraParams)))
	}
	if l.toolChoice != nil {
		opts = append(opts, openai.WithOpenAILLMToolChoice(l.toolChoice))
	}
	if l.httpClient != nil {
		l.inner = openai.NewOpenAILLMWithBaseURLAndHTTPClient(l.apiKey, l.model, l.baseURL, l.httpClient, opts...)
		return
	}
	l.inner = openai.NewOpenAILLMWithBaseURL(l.apiKey, l.model, l.baseURL, opts...)
}

func (l *MistralLLM) setExtraParam(key string, value any) {
	if l.extraParams == nil {
		l.extraParams = map[string]any{}
	}
	l.extraParams[key] = value
}

func cloneMistralLLMAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
