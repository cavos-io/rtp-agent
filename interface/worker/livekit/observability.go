package livekit

import (
	"time"

	"github.com/livekit/protocol/auth"
)

func NewObservabilityToken(apiKey, apiSecret string) (string, error) {
	return auth.NewAccessToken(apiKey, apiSecret).
		SetObservabilityGrant(&auth.ObservabilityGrant{Write: true}).
		SetValidFor(6 * time.Hour).
		ToJWT()
}
