package groq

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/cavos-io/rtp-agent/adapter/openai"
	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	defaultGroqLLMBaseURL = "https://api.groq.com/openai/v1"
	defaultGroqLLMModel   = "llama-3.3-70b-versatile"
)

type GroqLLM struct {
	inner           *openai.OpenAILLM
	apiKey          string
	baseURL         string
	reasoningEffort string
	httpClient      interface {
		Do(*http.Request) (*http.Response, error)
	}
}

type GroqLLMOption func(*GroqLLM)

func WithGroqLLMBaseURL(baseURL string) GroqLLMOption {
	return func(l *GroqLLM) {
		if baseURL != "" {
			l.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithGroqLLMReasoningEffort(reasoningEffort string) GroqLLMOption {
	return func(l *GroqLLM) {
		l.reasoningEffort = reasoningEffort
	}
}

func withGroqLLMHTTPClient(client interface {
	Do(*http.Request) (*http.Response, error)
}) GroqLLMOption {
	return func(l *GroqLLM) {
		l.httpClient = client
	}
}

func NewGroqLLM(apiKey string, model string, opts ...GroqLLMOption) *GroqLLM {
	resolvedAPIKey := resolveGroqAPIKey(apiKey)
	if model == "" {
		model = defaultGroqLLMModel
	}
	provider := &GroqLLM{
		apiKey:  resolvedAPIKey,
		baseURL: defaultGroqLLMBaseURL,
	}
	for _, opt := range opts {
		opt(provider)
	}
	if provider.reasoningEffort == "" {
		provider.reasoningEffort = defaultGroqLLMReasoningEffort(model)
	}
	openAIOpts := []openai.OpenAILLMOption{}
	if provider.reasoningEffort != "" {
		openAIOpts = append(openAIOpts, openai.WithOpenAILLMReasoningEffort(provider.reasoningEffort))
	}
	provider.inner = openai.NewOpenAILLMWithBaseURLAndHTTPClient(resolvedAPIKey, model, provider.baseURL, provider.httpClient, openAIOpts...)
	return provider
}

func resolveGroqAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("GROQ_API_KEY")
}

func (l *GroqLLM) Model() string { return l.inner.Model() }
func (l *GroqLLM) Provider() string {
	return "groq"
}

func defaultGroqLLMReasoningEffort(model string) string {
	switch model {
	case "openai/gpt-oss-120b", "openai/gpt-oss-20b":
		return "low"
	case "qwen/qwen3-32b":
		return "none"
	default:
		return ""
	}
}

func (l *GroqLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	if l.apiKey == "" {
		return nil, fmt.Errorf("groq API key is required, either as argument or set GROQ_API_KEY environmental variable")
	}
	return l.inner.Chat(ctx, chatCtx, opts...)
}

func (l *GroqLLM) Close() error {
	if l == nil || l.inner == nil {
		return nil
	}
	return l.inner.Close()
}
