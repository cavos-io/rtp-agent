package inference

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/livekit/protocol/auth"
)

const (
	defaultInferenceURL     = "https://agent-gateway.livekit.cloud/v1"
	stagingInferenceURL     = "https://agent-gateway.staging.livekit.cloud/v1"
	InferenceAccessTokenTTL = 10 * time.Minute

	HeaderUserAgent = "User-Agent"
	HeaderRoomID    = "X-LiveKit-Room-ID"
	HeaderJobID     = "X-LiveKit-Job-ID"
)

var inferenceContextHeadersProvider = struct {
	mu       sync.RWMutex
	provider func() map[string]string
}{}

func SetContextHeadersProvider(provider func() map[string]string) func() {
	inferenceContextHeadersProvider.mu.Lock()
	previous := inferenceContextHeadersProvider.provider
	inferenceContextHeadersProvider.provider = provider
	inferenceContextHeadersProvider.mu.Unlock()
	return func() {
		inferenceContextHeadersProvider.mu.Lock()
		inferenceContextHeadersProvider.provider = previous
		inferenceContextHeadersProvider.mu.Unlock()
	}
}

func InferenceHeaders() http.Header {
	headers := http.Header{}
	headers.Set(HeaderUserAgent, inferenceUserAgent())
	AddContextHeaders(headers)
	return headers
}

func AddContextHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	inferenceContextHeadersProvider.mu.RLock()
	provider := inferenceContextHeadersProvider.provider
	inferenceContextHeadersProvider.mu.RUnlock()
	if provider == nil {
		return
	}
	for key, value := range provider() {
		if key != "" && value != "" {
			headers.Set(key, value)
		}
	}
}

func inferenceUserAgent() string {
	return fmt.Sprintf("LiveKit Agents/Go (go %s)", runtime.Version())
}

func defaultInferenceWebsocketURL() string {
	inferenceURL := os.Getenv("LIVEKIT_INFERENCE_URL")
	if inferenceURL == "" {
		inferenceURL = defaultInferenceURL
		if strings.Contains(os.Getenv("LIVEKIT_URL"), ".staging.livekit.cloud") {
			inferenceURL = stagingInferenceURL
		}
	}
	return inferenceWebsocketURL(inferenceURL)
}

func inferenceWebsocketURL(baseURL string) string {
	if strings.HasPrefix(baseURL, "https://") {
		return "wss://" + strings.TrimPrefix(baseURL, "https://")
	}
	if strings.HasPrefix(baseURL, "http://") {
		return "ws://" + strings.TrimPrefix(baseURL, "http://")
	}
	return baseURL
}

func resolveInferenceCredentials(apiKey string, apiSecret string) (string, string) {
	if apiKey == "" {
		apiKey = os.Getenv("LIVEKIT_INFERENCE_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("LIVEKIT_API_KEY")
		}
	}
	if apiSecret == "" {
		apiSecret = os.Getenv("LIVEKIT_INFERENCE_API_SECRET")
		if apiSecret == "" {
			apiSecret = os.Getenv("LIVEKIT_API_SECRET")
		}
	}
	return apiKey, apiSecret
}

func CreateAccessToken(apiKey, apiSecret string, ttl time.Duration) (string, error) {
	at := auth.NewAccessToken(apiKey, apiSecret)
	grant := &auth.InferenceGrant{
		Perform: true,
	}
	at.SetInferenceGrant(grant).
		SetIdentity("agent").
		SetValidFor(ttl)

	return at.ToJWT()
}
