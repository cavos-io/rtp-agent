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
	base goopenai.HTTPDoer
}

func (c *liveKitInferenceHeadersHTTPClient) Do(req *http.Request) (*http.Response, error) {
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set("User-Agent", liveKitInferenceUserAgent())
	inference.AddContextHeaders(cloned.Header)
	return base.Do(cloned)
}

type LiveKitInferenceLLM struct {
	model      string
	apiKey     string
	apiSecret  string
	baseURL    string
	httpClient goopenai.HTTPDoer
}

var _ llm.LLM = (*LiveKitInferenceLLM)(nil)

func NewLiveKitInferenceLLM(model string, apiKey, apiSecret string) (*LiveKitInferenceLLM, error) {
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
	return &LiveKitInferenceLLM{
		model:     model,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		baseURL:   liveKitInferenceLLMURL(),
	}, nil
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

	inner := NewOpenAILLMWithBaseURLAndHTTPClient(token, l.model, l.baseURL, &liveKitInferenceHeadersHTTPClient{base: l.httpClient})
	return inner.Chat(ctx, chatCtx, opts...)
}
