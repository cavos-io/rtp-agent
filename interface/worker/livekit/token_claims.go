package livekit

import (
	"net/http"
	"time"

	"github.com/go-jose/go-jose/v3"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func WorkerAuthToken(apiKey string, apiSecret string, ttl time.Duration) (string, error) {
	return auth.NewAccessToken(apiKey, apiSecret).
		SetVideoGrant(&auth.VideoGrant{Agent: true}).
		SetValidFor(ttl).
		ToJWT()
}

func WorkerAuthHeader(token string) http.Header {
	return http.Header{"Authorization": []string{"Bearer " + token}}
}

func LocalAgentToken(apiKey string, apiSecret string, identity string, room string, ttl time.Duration) (string, error) {
	return auth.NewAccessToken(apiKey, apiSecret).
		SetIdentity(identity).
		SetKind(lkprotocol.ParticipantInfo_AGENT).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     room,
			Agent:    true,
		}).
		SetValidFor(ttl).
		ToJWT()
}

func TokenClaims(token string) (*auth.ClaimGrants, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return nil, err
	}
	claims := &auth.ClaimGrants{}
	if err := tok.UnsafeClaimsWithoutVerification(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func TokenIdentity(token string) (string, error) {
	verifier, err := auth.ParseAPIToken(token)
	if err != nil {
		return "", err
	}
	return verifier.Identity(), nil
}

func RefreshToken(token string, apiSecret string, now time.Time, ttl time.Duration) (string, error) {
	tok, err := jwt.ParseSigned(token)
	if err != nil {
		return "", err
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte(apiSecret), &standardClaims, &grants); err != nil {
		return "", err
	}
	standardClaims.Expiry = jwt.NewNumericDate(now.Add(ttl))

	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(apiSecret)}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", err
	}
	return jwt.Signed(signer).Claims(standardClaims).Claims(grants).CompactSerialize()
}

func LocalParticipantIdentity(token string, fallbackIdentity string) string {
	claims, err := TokenClaims(token)
	if err == nil && claims.Identity != "" {
		return claims.Identity
	}
	return fallbackIdentity
}

func LocalJobIdentity(token string, explicitIdentity string, newIdentity func(string) string) string {
	if token != "" {
		if identity, err := TokenIdentity(token); err == nil && identity != "" {
			return identity
		}
	}
	if explicitIdentity != "" {
		return explicitIdentity
	}
	if newIdentity == nil {
		return ""
	}
	return newIdentity("fake-agent-")
}
