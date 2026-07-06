package telnyx

import (
	"context"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

const (
	defaultTelnyxLLMURL   = "https://api.telnyx.com/v2/ai"
	defaultTelnyxLLMModel = "meta-llama/Meta-Llama-3.1-70B-Instruct"
)

type TelnyxLLM struct {
	inner   *openai.OpenAILLM
	err     error
	baseURL string
}

type telnyxLLMOptions struct {
	baseURL    string
	httpClient openaisdk.HTTPDoer
	llmOptions []openai.OpenAILLMOption
}

type TelnyxLLMOption func(*telnyxLLMOptions)

func WithTelnyxLLMBaseURL(baseURL string) TelnyxLLMOption {
	return func(o *telnyxLLMOptions) {
		if baseURL != "" {
			o.baseURL = baseURL
		}
	}
}

func WithTelnyxLLMHTTPClient(httpClient openaisdk.HTTPDoer) TelnyxLLMOption {
	return func(o *telnyxLLMOptions) {
		o.httpClient = httpClient
	}
}

func WithTelnyxLLMOptions(opts ...openai.OpenAILLMOption) TelnyxLLMOption {
	return func(o *telnyxLLMOptions) {
		o.llmOptions = append(o.llmOptions, opts...)
	}
}

func NewTelnyxLLM(apiKey string, model string, opts ...TelnyxLLMOption) *TelnyxLLM {
	options := telnyxLLMOptions{baseURL: defaultTelnyxLLMURL}
	for _, opt := range opts {
		opt(&options)
	}
	inner, err := newTelnyxOpenAILLM(apiKey, model, options)
	return &TelnyxLLM{
		inner:   inner,
		err:     err,
		baseURL: options.baseURL,
	}
}

func newTelnyxOpenAILLM(apiKey string, model string, options telnyxLLMOptions) (*openai.OpenAILLM, error) {
	if model == "" {
		model = defaultTelnyxLLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("telnyx AI API key is required, either as argument or set TELNYX_API_KEY environmental variable")
	}
	llmOptions := append([]openai.OpenAILLMOption{openai.WithOpenAILLMToolChoice("auto")}, options.llmOptions...)
	return openai.NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, options.baseURL, options.httpClient, llmOptions...), nil
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
