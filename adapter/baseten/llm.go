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

type BasetenLLM struct {
	inner   *adapteropenai.OpenAILLM
	baseURL string
}

func NewBasetenLLM(apiKey string, model string) (*BasetenLLM, error) {
	return newBasetenLLMWithBaseURL(apiKey, model, defaultBasetenLLMURL)
}

func newBasetenLLMWithBaseURL(apiKey string, model string, baseURL string) (*BasetenLLM, error) {
	return newBasetenLLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, nil)
}

func newBasetenLLMWithBaseURLAndHTTPClient(apiKey string, model string, baseURL string, httpClient openaisdk.HTTPDoer) (*BasetenLLM, error) {
	if apiKey == "" {
		apiKey = os.Getenv(basetenAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("BASETEN_API_KEY is required, either as argument or set BASETEN_API_KEY environment variable")
	}
	if model == "" {
		model = defaultBasetenLLMModel
	}
	return &BasetenLLM{
		inner:   adapteropenai.NewOpenAILLMWithBaseURLAndHTTPClient(apiKey, model, baseURL, httpClient),
		baseURL: baseURL,
	}, nil
}

func (l *BasetenLLM) Model() string { return l.inner.Model() }
func (l *BasetenLLM) Provider() string {
	return "Baseten"
}

func (l *BasetenLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	return l.inner.Chat(ctx, chatCtx, opts...)
}
