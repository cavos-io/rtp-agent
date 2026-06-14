package livekit

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/library/inferencecontext"
	"github.com/livekit/protocol/auth"
)

const (
	defaultInferenceURL     = "https://agent-gateway.livekit.cloud/v1"
	stagingInferenceURL     = "https://agent-gateway.staging.livekit.cloud/v1"
	InferenceAccessTokenTTL = 10 * time.Minute

	HeaderUserAgent         = "User-Agent"
	HeaderRoomID            = inferencecontext.HeaderRoomID
	HeaderJobID             = inferencecontext.HeaderJobID
	HeaderInferenceProvider = inferencecontext.HeaderInferenceProvider
	HeaderInferencePriority = inferencecontext.HeaderInferencePriority
)

func SetContextHeadersProvider(provider func() map[string]string) func() {
	return inferencecontext.SetHeadersProvider(provider)
}

func InferenceHeaders() http.Header {
	headers := http.Header{}
	headers.Set(HeaderUserAgent, inferenceUserAgent())
	AddContextHeaders(headers)
	return headers
}

func AddContextHeaders(headers http.Header) {
	inferencecontext.AddHeaders(headers)
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
