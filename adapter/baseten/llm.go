package baseten

import (
	"context"
	"fmt"
	"os"

	adapteropenai "github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
	openaisdk "github.com/sashabaranov/go-openai"
)

const (
	basetenAPIKeyEnv       = "BASETEN_API_KEY"
	defaultBasetenLLMModel = "meta-llama/Llama-4-Maverick-17B-128E-Instruct"
	defaultBasetenLLMURL   = "https://inference.baseten.co/v1"
)

type LLM struct {
	inner   *adapteropenai.LLM
	baseURL string
}

func NewLLM(apiKey string, model string) (*LLM, error) {
	return newBasetenLLMWithBaseURL(apiKey, model, defaultBasetenLLMURL)
}

func newBasetenLLMWithBaseURL(apiKey string, model string, baseURL string) (*LLM, error) {
	return newBasetenLLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, nil)
}

func newBasetenLLMWithBaseURLAndHTTPClient(apiKey string, model string, baseURL string, httpClient openaisdk.HTTPDoer) (*LLM, error) {
	if apiKey == "" {
		apiKey = os.Getenv(basetenAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("BASETEN_API_KEY is required, either as argument or set BASETEN_API_KEY environment variable")
	}
	if model == "" {
		model = defaultBasetenLLMModel
	}
	return &LLM{
		inner:   adapteropenai.NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, httpClient),
		baseURL: baseURL,
	}, nil
}

func (l *LLM) Model() string { return l.inner.Model() }
func (l *LLM) Provider() string {
	return "Baseten"
}

func (l *LLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}

// Deprecated: use LLM.
type BasetenLLM = LLM

// Deprecated: use NewLLM.
func NewBasetenLLM(apiKey string, model string) (*LLM, error) {
	return NewLLM(apiKey, model)
}
