package inference

import (
	"time"

	"github.com/livekit/protocol/auth"
)

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
