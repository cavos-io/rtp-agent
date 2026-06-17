package livekit

import (
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
)

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

func LocalParticipantIdentity(token string, fallbackIdentity string) string {
	claims, err := TokenClaims(token)
	if err == nil && claims.Identity != "" {
		return claims.Identity
	}
	return fallbackIdentity
}
