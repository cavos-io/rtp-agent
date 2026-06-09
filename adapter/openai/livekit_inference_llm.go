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
	extraHeaders      http.Header
	provider          string
	inferencePriority string
}

func (c *liveKitInferenceHeadersHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	for key, values := range c.extraHeaders {
		for _, value := range values {
			cloned.Header.Add(key, value)
		}
	}
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
	extraParams    map[string]any
}

var _ llm.LLM = (*LiveKitInferenceLLM)(nil)

type LiveKitInferenceLLMOption func(*LiveKitInferenceLLM)

func WithLiveKitInferenceLLMModel(model string) LiveKitInferenceLLMOption {
	return func(l *LiveKitInferenceLLM) {
		if model != "" {
			l.model = model
		}
	}
}

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

func WithLiveKitInferenceLLMExtraParams(params map[string]any) LiveKitInferenceLLMOption {
	return func(l *LiveKitInferenceLLM) {
		l.extraParams = cloneLiveKitInferenceLLMExtraParams(params)
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

func (l *LiveKitInferenceLLM) UpdateOptions(opts ...LiveKitInferenceLLMOption) {
	for _, opt := range opts {
		opt(l)
	}
}

func (l *LiveKitInferenceLLM) Chat(ctx context.Context, chatCtx *llm.ChatContext, opts ...llm.ChatOption) (llm.LLMStream, error) {
	token, err := inference.CreateAccessToken(l.apiKey, l.apiSecret, inference.InferenceAccessTokenTTL)
	if err != nil {
		return nil, err
	}

	inferencePriority := l.inferenceClass
	callHeaders, chatOpts := liveKitInferenceCallHeadersAndOptions(opts)
	if priority := liveKitInferenceClassFromOptions(opts); priority != "" {
		inferencePriority = priority
	}
	extraHeaders, extraParams := liveKitInferenceExtraHeadersFromParams(l.extraParams)
	if len(extraHeaders) == 0 {
		extraHeaders = callHeaders
	}
	extraParams = liveKitInferenceLLMStreamOptions(extraParams)
	inner := NewOpenAILLMWithBaseURLAndHTTPClient(token, l.model, l.baseURL, &liveKitInferenceHeadersHTTPClient{
		base:              l.httpClient,
		extraHeaders:      extraHeaders,
		provider:          l.provider,
		inferencePriority: inferencePriority,
	}, func(inner *OpenAILLM) {
		inner.extraParams = extraParams
		inner.defaultReasoning = false
	})
	return inner.Chat(ctx, chatCtx, chatOpts...)
}

func liveKitInferenceLLMStreamOptions(params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	switch options := params["stream_options"].(type) {
	case map[string]any:
		options["include_usage"] = true
	default:
		params["stream_options"] = map[string]any{"include_usage": true}
	}
	return params
}

func liveKitInferenceClassFromOptions(opts []llm.ChatOption) string {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}
	value, ok := options.ExtraParams["inference_class"]
	if !ok {
		return ""
	}
	priority, ok := value.(string)
	if !ok || priority == "" {
		return ""
	}
	return priority
}

func liveKitInferenceCallHeadersAndOptions(opts []llm.ChatOption) (http.Header, []llm.ChatOption) {
	options := &llm.ChatOptions{}
	for _, opt := range opts {
		opt(options)
	}
	if len(options.ExtraParams) == 0 {
		return nil, opts
	}
	headers, filteredParams := liveKitInferenceExtraHeadersFromParams(options.ExtraParams)
	if _, ok := filteredParams["inference_class"]; !ok && headers == nil {
		return nil, opts
	}
	if _, ok := filteredParams["inference_class"]; ok {
		filtered := make(map[string]any, len(filteredParams)-1)
		for key, value := range filteredParams {
			if key != "inference_class" {
				filtered[key] = value
			}
		}
		filteredParams = filtered
	}
	filteredOpts := make([]llm.ChatOption, 0, len(opts)+1)
	filteredOpts = append(filteredOpts, opts...)
	filteredOpts = append(filteredOpts, llm.WithExtraParams(filteredParams))
	return headers, filteredOpts
}

func liveKitInferenceExtraHeadersFromParams(params map[string]any) (http.Header, map[string]any) {
	extraParams := cloneLiveKitInferenceLLMExtraParams(params)
	if len(extraParams) == 0 {
		return nil, extraParams
	}
	rawHeaders, ok := extraParams["extra_headers"]
	if !ok {
		return nil, extraParams
	}
	delete(extraParams, "extra_headers")
	headers := http.Header{}
	switch typed := rawHeaders.(type) {
	case http.Header:
		for key, values := range typed {
			for _, value := range values {
				headers.Add(key, value)
			}
		}
	case map[string]string:
		for key, value := range typed {
			headers.Set(key, value)
		}
	case map[string]any:
		for key, value := range typed {
			if text, ok := value.(string); ok {
				headers.Set(key, text)
			}
		}
	}
	if len(headers) == 0 {
		return nil, extraParams
	}
	return headers, extraParams
}

func cloneLiveKitInferenceLLMExtraParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	clone := make(map[string]any, len(params))
	for key, value := range params {
		clone[key] = value
	}
	return clone
}
