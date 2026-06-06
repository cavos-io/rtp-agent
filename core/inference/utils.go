package inference

import (
	"os"
	"time"

	"github.com/livekit/protocol/auth"
)

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
