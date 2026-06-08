package openai

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/cavos-io/rtp-agent/core/inference"
	"github.com/cavos-io/rtp-agent/core/llm"
	goopenai "github.com/sashabaranov/go-openai"
)

const (
	defaultLiveKitInferenceLLMURL = "https://agent-gateway.livekit.cloud/v1"
	stagingLiveKitInferenceLLMURL = "https://agent-gateway.staging.livekit.cloud/v1"

	liveKitInferenceAPIKeyEnv    = "LIVEKIT_INFERENCE_API_KEY"
	liveKitInferenceAPISecretEnv = "LIVEKIT_INFERENCE_API_SECRET"
	liveKitInferenceURLEnv       = "LIVEKIT_INFERENCE_URL"
	liveKitAPIKeyEnv             = "LIVEKIT_API_KEY"
	liveKitAPISecretEnv          = "LIVEKIT_API_SECRET"
	liveKitURLEnv                = "LIVEKIT_URL"
)

type liveKitInferenceHeadersHTTPClient struct {
	base              goopenai.HTTPDoer
	provider          string
	inferencePriority string
}

func (c *liveKitInferenceHeadersHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", liveKitInferenceUserAgent())
	inference.AddContextHeaders(cloned.Header)
	if c.provider != "" {
		cloned.Header.Set(inference.HeaderInferenceProvider, c.provider)
	}
	if c.inferencePriority != "" {
		cloned.Header.Set(inference.HeaderInferencePriority, c.inferencePriority)
	}
	return base.Do(cloned)
}

type LiveKitInferenceLLM struct {
	model          string
	apiKey         string
	apiSecret      string
	baseURL        string
	httpClient     goopenai.HTTPDoer
	provider       string
	inferenceClass string
}

var _ llm.LLM = (*LiveKitInferenceLLM)(nil)

type LiveKitInferenceLLMOption func(*LiveKitInferenceLLM)

func WithLiveKitInferenceLLMProvider(provider string) LiveKitInferenceLLMOption {
	return func(l *LiveKitInferenceLLM) {
		l.provider = provider
	}
}

func WithLiveKitInferenceLLMClass(inferenceClass string) LiveKitInferenceLLMOption {
	return func(l *LiveKitInferenceLLM) {
		l.inferenceClass = inferenceClass
	}
}

func NewLiveKitInferenceLLM(model string, apiKey, apiSecret string, opts ...LiveKitInferenceLLMOption) (*LiveKitInferenceLLM, error) {
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if apiKey == "" {
		apiKey = os.Getenv(liveKitInferenceAPIKeyEnv)
	}
	if apiKey == "" {
		apiKey = os.Getenv(liveKitAPIKeyEnv)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("LIVEKIT_API_KEY is required, either as argument or set LIVEKIT_INFERENCE_API_KEY or LIVEKIT_API_KEY environment variable")
	}
	if apiSecret == "" {
		apiSecret = os.Getenv(liveKitInferenceAPISecretEnv)
	}
	if apiSecret == "" {
		apiSecret = os.Getenv(liveKitAPISecretEnv)
	}
	if apiSecret == "" {
		return nil, fmt.Errorf("LIVEKIT_API_SECRET is required, either as argument or set LIVEKIT_INFERENCE_API_SECRET or LIVEKIT_API_SECRET environment variable")
	}
	provider := &LiveKitInferenceLLM{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   liveKitInferenceLLMURL(),
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider, nil
}

func liveKitInferenceLLMURL() string {
	if url := os.Getenv(liveKitInferenceURLEnv); url != "" {
		return url
	}
	if livekitURL := os.Getenv(liveKitURLEnv); strings.Contains(livekitURL, ".staging.livekit.cloud") {
		return stagingLiveKitInferenceLLMURL
	}
	return defaultLiveKitInferenceLLMURL
}

func liveKitInferenceUserAgent() string {
	return fmt.Sprintf("LiveKit Agents/%s (go %s)", PluginVersion, runtime.Version())
}

func (l *LiveKitInferenceLLM) Model() string {
	return l.model
}

func (l *LiveKitInferenceLLM) Provider() string {
	return "livekit"
}

func (l *LiveKitInferenceLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	token, err := inference.CreateAccessToken(l.apiKey, l.apiSecret, inference.InferenceAccessTokenTTL)
	if err != nil {
		return nil, err
	}

	inferencePriority, chatOpts := l.inferenceClass, opts
	if priority, filtered, ok := liveKitInferenceClassFromChatOptions(opts); ok {
		inferencePriority = priority
		chatOpts = filtered
	}
	inner := NewOpenAILLMWithBaseURLAndHTTPClient(token, l.model, l.baseURL, &liveKitInferenceHeadersHTTPClient{
		base:              l.httpClient,
		provider:          l.provider,
		inferencePriority: inferencePriority,
	})
	return inner.Chat(ctx, chatCtx, chatOpts...)
}

func liveKitInferenceClassFromChatOptions(opts []llm.ChatOption) (string, []llm.ChatOption, bool) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}
	value, ok := options.ExtraParams["inference_class"]
	if !ok {
		return "", opts, false
	}
	priority, ok := value.(string)
	if !ok || priority == "" {
		return "", opts, false
	}
	filteredParams := make(map[string]any, len(options.ExtraParams)-1)
	for key, value := range options.ExtraParams {
		if key != "inference_class" {
			filteredParams[key] = value
		}
	}
	filtered := make([]llm.ChatOption, 0, len(opts)+1)
	filtered = append(filtered, opts...)
	filtered = append(filtered, llm.WithExtraParams(filteredParams))
	return priority, filtered, true
}
