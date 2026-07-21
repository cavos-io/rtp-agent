package telnyx

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

const (
	defaultTelnyxLLMURL   = "https://api.telnyx.com/v2/ai"
	defaultTelnyxLLMModel = "meta-llama/Meta-Llama-3.1-70B-Instruct"
)

type LLM struct {
	inner   *openai.LLM
	err     error
	baseURL string
}

type telnyxLLMOptions struct {
	baseURL    string
	httpClient openaisdk.HTTPDoer
	llmOptions []openai.LLMOption
}

type LLMOption func(*telnyxLLMOptions)

func WithTelnyxLLMBaseURL(baseURL string) LLMOption {
	return func(o *telnyxLLMOptions) {
		if baseURL != "" {
			o.baseURL = baseURL
		}
	}
}

func WithTelnyxLLMHTTPClient(httpClient openaisdk.HTTPDoer) LLMOption {
	return func(o *telnyxLLMOptions) {
		o.httpClient = httpClient
	}
}

func WithTelnyxLLMOptions(opts ...openai.LLMOption) LLMOption {
	return func(o *telnyxLLMOptions) {
		o.llmOptions = append(o.llmOptions, opts...)
	}
}

func NewLLM(apiKey string, model string, opts ...LLMOption) *LLM {
	options := telnyxLLMOptions{baseURL: defaultTelnyxLLMURL}
	for _, opt := range opts {
		opt(&options)
	}
	inner, err := newTelnyxOpenAILLM(apiKey, model, options)
	return &LLM{
		inner:   inner,
		err:     err,
		baseURL: options.baseURL,
	}
}

func newTelnyxOpenAILLM(apiKey string, model string, options telnyxLLMOptions) (*openai.LLM, error) {
	if model == "" {
		model = defaultTelnyxLLMModel
	}
	if apiKey == "" {
		apiKey = os.Getenv(telnyxAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("telnyx AI API key is required, either as argument or set TELNYX_API_KEY environmental variable")
	}
	llmOptions := append([]openai.LLMOption{openai.WithOpenAILLMToolChoice("auto")}, options.llmOptions...)
	return openai.NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, options.baseURL, options.httpClient, llmOptions...), nil
}

func (l *LLM) Model() string {
	if l.inner == nil {
		return ""
	}
	return l.inner.Model()
}

func (l *LLM) BaseURL() string {
	return l.baseURL
}

func (l *LLM) Provider() string {
	if l == nil {
		return ""
	}
	u, err := url.Parse(l.baseURL)
	if err != nil || u.Host == "" {
		return l.baseURL
	}
	return u.Host
}

func (l *LLM) Close() error {
	if l == nil || l.inner == nil {
		return nil
	}
	return l.inner.Close()
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}

// Deprecated: use LLM.
type TelnyxLLM = LLM

// Deprecated: use LLMOption.
type TelnyxLLMOption = LLMOption

// Deprecated: use NewLLM.
func NewTelnyxLLM(apiKey string, model string, opts ...LLMOption) *LLM {
	return NewLLM(apiKey, model, opts...)
}
