package livekit_test

import (
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/livekit/protocol/auth"
)

func TestTokenClaimsReturnsUnverifiedTokenClaims(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		SetName("Token Agent").
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     "room-a",
			Agent:    true,
		}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	claims, err := workerlivekit.TokenClaims(token)
	if err != nil {
		t.Fatalf("TokenClaims() error = %v", err)
	}
	if claims.Identity != "token-agent" {
		t.Fatalf("TokenClaims().Identity = %q, want token-agent", claims.Identity)
	}
	if claims.Name != "Token Agent" {
		t.Fatalf("TokenClaims().Name = %q, want Token Agent", claims.Name)
	}
	if claims.Video == nil {
		t.Fatal("TokenClaims().Video = nil, want video grant")
	}
	if !claims.Video.RoomJoin {
		t.Fatal("TokenClaims().Video.RoomJoin = false, want true")
	}
	if !claims.Video.Agent {
		t.Fatal("TokenClaims().Video.Agent = false, want true")
	}
	if claims.Video.Room != "room-a" {
		t.Fatalf("TokenClaims().Video.Room = %q, want room-a", claims.Video.Room)
	}
}

func TestLocalParticipantIdentityPrefersTokenIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	if got := workerlivekit.LocalParticipantIdentity(token, "accepted-agent"); got != "token-agent" {
		t.Fatalf("LocalParticipantIdentity() = %q, want token-agent", got)
	}
}

func TestLocalParticipantIdentityFallsBackWhenTokenInvalidOrEmpty(t *testing.T) {
	if got := workerlivekit.LocalParticipantIdentity("", "accepted-agent"); got != "accepted-agent" {
		t.Fatalf("LocalParticipantIdentity(empty token) = %q, want accepted-agent", got)
	}
	if got := workerlivekit.LocalParticipantIdentity("not-a-jwt", "accepted-agent"); got != "accepted-agent" {
		t.Fatalf("LocalParticipantIdentity(invalid token) = %q, want accepted-agent", got)
	}
}

func TestWorkerAuthTokenCarriesAgentGrant(t *testing.T) {
	token, err := workerlivekit.WorkerAuthToken("api-key", "api-secret", time.Hour)
	if err != nil {
		t.Fatalf("WorkerAuthToken() error = %v", err)
	}

	claims, err := workerlivekit.TokenClaims(token)
	if err != nil {
		t.Fatalf("TokenClaims() error = %v", err)
	}
	if claims.Video == nil {
		t.Fatal("TokenClaims().Video = nil, want video grant")
	}
	if !claims.Video.Agent {
		t.Fatal("TokenClaims().Video.Agent = false, want true")
	}
	if claims.Video.RoomJoin {
		t.Fatal("TokenClaims().Video.RoomJoin = true, want false")
	}
}
