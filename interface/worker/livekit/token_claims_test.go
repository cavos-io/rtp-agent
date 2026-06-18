package livekit_test

import (
	"strings"
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/go-jose/go-jose/v3/jwt"
	"github.com/livekit/protocol/auth"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestClaimGrantsAliasUsesLiveKitAuthType(t *testing.T) {
	claims := &workerlivekit.ClaimGrants{Identity: "agent-a"}
	var authClaims *auth.ClaimGrants = claims

	if authClaims.Identity != "agent-a" {
		t.Fatalf("ClaimGrants alias identity = %q, want agent-a", authClaims.Identity)
	}
}

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

func TestLocalJobIdentityPrefersValidTokenIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	got := workerlivekit.LocalJobIdentity(token, "explicit-agent", func(string) string {
		t.Fatal("new identity was called")
		return ""
	})
	if got != "token-agent" {
		t.Fatalf("LocalJobIdentity() = %q, want token-agent", got)
	}
}

func TestLocalJobIdentityUsesExplicitIdentityWhenTokenInvalid(t *testing.T) {
	got := workerlivekit.LocalJobIdentity("not-a-jwt", "explicit-agent", func(string) string {
		t.Fatal("new identity was called")
		return ""
	})
	if got != "explicit-agent" {
		t.Fatalf("LocalJobIdentity() = %q, want explicit-agent", got)
	}
}

func TestLocalJobIdentityGeneratesFakeAgentIdentityWhenMissing(t *testing.T) {
	got := workerlivekit.LocalJobIdentity("", "", func(prefix string) string {
		if prefix != "fake-agent-" {
			t.Fatalf("LocalJobIdentity() prefix = %q, want fake-agent-", prefix)
		}
		return "fake-agent-id"
	})
	if got != "fake-agent-id" {
		t.Fatalf("LocalJobIdentity() = %q, want fake-agent-id", got)
	}
}

func TestLocalJobTokenIdentityReturnsParsedIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	identity, err := workerlivekit.LocalJobTokenIdentity(token)
	if err != nil {
		t.Fatalf("LocalJobTokenIdentity() error = %v", err)
	}
	if identity != "token-agent" {
		t.Fatalf("LocalJobTokenIdentity() = %q, want token-agent", identity)
	}
}

func TestLocalJobTokenIdentityRejectsInvalidToken(t *testing.T) {
	_, err := workerlivekit.LocalJobTokenIdentity("not-a-jwt")
	if err == nil {
		t.Fatal("LocalJobTokenIdentity(invalid token) error = nil, want error")
	}
}

func TestLocalJobParticipantIdentityForRunPrefersToken(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	identity, err := workerlivekit.LocalJobParticipantIdentityForRun(token, "explicit-agent")
	if err != nil {
		t.Fatalf("LocalJobParticipantIdentityForRun() error = %v", err)
	}
	if identity != "token-agent" {
		t.Fatalf("LocalJobParticipantIdentityForRun() = %q, want token-agent", identity)
	}
}

func TestLocalJobParticipantIdentityForRunWrapsInvalidToken(t *testing.T) {
	_, err := workerlivekit.LocalJobParticipantIdentityForRun("not-a-jwt", "explicit-agent")
	if err == nil {
		t.Fatal("LocalJobParticipantIdentityForRun(invalid token) error = nil, want error")
	}
	if got := err.Error(); !strings.Contains(got, "invalid local job token") {
		t.Fatalf("LocalJobParticipantIdentityForRun(invalid token) error = %q, want invalid local job token", got)
	}
}

func TestLocalJobTokenPreservesProvidedToken(t *testing.T) {
	token, err := workerlivekit.LocalJobToken("provided-token", "api-key", "api-secret", "agent-a", "room-a", time.Hour)
	if err != nil {
		t.Fatalf("LocalJobToken() error = %v", err)
	}
	if token != "provided-token" {
		t.Fatalf("LocalJobToken() = %q, want provided-token", token)
	}
}

func TestLocalJobTokenReturnsEmptyWithoutCredentials(t *testing.T) {
	token, err := workerlivekit.LocalJobToken("", "", "api-secret", "agent-a", "room-a", time.Hour)
	if err != nil {
		t.Fatalf("LocalJobToken() error = %v", err)
	}
	if token != "" {
		t.Fatalf("LocalJobToken() = %q, want empty", token)
	}
}

func TestLocalJobTokenGeneratesAgentJoinToken(t *testing.T) {
	token, err := workerlivekit.LocalJobToken("", "api-key", "api-secret", "agent-a", "room-a", time.Hour)
	if err != nil {
		t.Fatalf("LocalJobToken() error = %v", err)
	}
	if token == "" {
		t.Fatal("LocalJobToken() = empty, want generated token")
	}
	identity, err := workerlivekit.TokenIdentity(token)
	if err != nil {
		t.Fatalf("TokenIdentity(generated token) error = %v", err)
	}
	if identity != "agent-a" {
		t.Fatalf("generated token identity = %q, want agent-a", identity)
	}
}

func TestTokenIdentityReturnsParsedAPIIdentity(t *testing.T) {
	token, err := auth.NewAccessToken("key", "secret").
		SetIdentity("token-agent").
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}

	identity, err := workerlivekit.TokenIdentity(token)
	if err != nil {
		t.Fatalf("TokenIdentity() error = %v", err)
	}
	if identity != "token-agent" {
		t.Fatalf("TokenIdentity() = %q, want token-agent", identity)
	}
}

func TestTokenIdentityRejectsInvalidToken(t *testing.T) {
	_, err := workerlivekit.TokenIdentity("not-a-jwt")
	if err == nil {
		t.Fatal("TokenIdentity(invalid token) error = nil, want error")
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

func TestWorkerAuthHeaderUsesBearerToken(t *testing.T) {
	header := workerlivekit.WorkerAuthHeader("worker-token")
	if got := header.Get("Authorization"); got != "Bearer worker-token" {
		t.Fatalf("Authorization header = %q, want bearer token", got)
	}
}

func TestLocalAgentTokenCarriesRoomJoinAgentGrant(t *testing.T) {
	token, err := workerlivekit.LocalAgentToken("api-key", "api-secret", "local-agent", "room-a", time.Hour)
	if err != nil {
		t.Fatalf("LocalAgentToken() error = %v", err)
	}

	claims, err := workerlivekit.TokenClaims(token)
	if err != nil {
		t.Fatalf("TokenClaims() error = %v", err)
	}
	if claims.Identity != "local-agent" {
		t.Fatalf("TokenClaims().Identity = %q, want local-agent", claims.Identity)
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

func TestRefreshTokenPreservesGrantsAndUpdatesExpiry(t *testing.T) {
	original, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetMetadata("metadata-a").
		SetKind(lkprotocol.ParticipantInfo_AGENT).
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: "room-a", Agent: true}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}
	now := time.Unix(1700000000, 0)

	refreshed, err := workerlivekit.RefreshToken(original, "api-secret", now, time.Hour)
	if err != nil {
		t.Fatalf("RefreshToken() error = %v", err)
	}
	if refreshed == "" || refreshed == original {
		t.Fatal("RefreshToken() did not return a new token")
	}

	tok, err := jwt.ParseSigned(refreshed)
	if err != nil {
		t.Fatalf("ParseSigned(refreshed) error = %v", err)
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
		t.Fatalf("refreshed token Claims() error = %v", err)
	}
	if standardClaims.Expiry == nil {
		t.Fatal("refreshed token expiry = nil, want expiry")
	}
	if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("refreshed token expiry = %v, want %v", got, now.Add(time.Hour))
	}
	if grants.Identity != "agent-a" {
		t.Fatalf("refreshed identity = %q, want agent-a", grants.Identity)
	}
	if grants.Name != "Agent A" {
		t.Fatalf("refreshed name = %q, want Agent A", grants.Name)
	}
	if grants.Metadata != "metadata-a" {
		t.Fatalf("refreshed metadata = %q, want metadata-a", grants.Metadata)
	}
	if grants.GetParticipantKind() != lkprotocol.ParticipantInfo_AGENT {
		t.Fatalf("refreshed kind = %v, want AGENT", grants.GetParticipantKind())
	}
	if grants.Video == nil || !grants.Video.RoomJoin || !grants.Video.Agent || grants.Video.Room != "room-a" {
		t.Fatalf("refreshed video grant = %#v, want room join agent grant", grants.Video)
	}
}

func TestRefreshRunningJobTokenForReloadRefreshesTokenAndPreservesFields(t *testing.T) {
	original, err := auth.NewAccessToken("api-key", "api-secret").
		SetIdentity("agent-a").
		SetName("Agent A").
		SetVideoGrant(&auth.VideoGrant{RoomJoin: true, Room: "room-a", Agent: true}).
		ToJWT()
	if err != nil {
		t.Fatalf("ToJWT() error = %v", err)
	}
	now := time.Unix(1700001000, 0)
	job := &lkprotocol.Job{Id: "job-a"}

	info, err := workerlivekit.RefreshRunningJobTokenForReload(workerlivekit.RunningJobInfo{
		AcceptArguments: workerlivekit.JobAcceptArguments{
			Name:       "accepted-name",
			Identity:   "accepted-agent",
			Metadata:   "metadata-a",
			Attributes: map[string]string{"tier": "gold"},
		},
		Job:      job,
		URL:      "wss://livekit.example",
		Token:    original,
		WorkerID: "worker-a",
		FakeJob:  true,
	}, "api-secret", now)
	if err != nil {
		t.Fatalf("RefreshRunningJobTokenForReload() error = %v", err)
	}
	if info.Token == "" || info.Token == original {
		t.Fatal("RefreshRunningJobTokenForReload() did not return a refreshed token")
	}
	if info.Job != job || info.URL != "wss://livekit.example" || info.WorkerID != "worker-a" || !info.FakeJob {
		t.Fatalf("RefreshRunningJobTokenForReload() info = %#v, want fields preserved", info)
	}
	if info.AcceptArguments.Attributes["tier"] != "gold" {
		t.Fatalf("refreshed accept tier = %q, want gold", info.AcceptArguments.Attributes["tier"])
	}

	tok, err := jwt.ParseSigned(info.Token)
	if err != nil {
		t.Fatalf("ParseSigned(refreshed) error = %v", err)
	}
	standardClaims := jwt.Claims{}
	grants := auth.ClaimGrants{}
	if err := tok.Claims([]byte("api-secret"), &standardClaims, &grants); err != nil {
		t.Fatalf("refreshed token Claims() error = %v", err)
	}
	if got := standardClaims.Expiry.Time(); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("refreshed token expiry = %v, want %v", got, now.Add(time.Hour))
	}
	if grants.Identity != "agent-a" {
		t.Fatalf("refreshed identity = %q, want agent-a", grants.Identity)
	}
}

func TestRefreshRunningJobsForReloadRequiresAPISecret(t *testing.T) {
	_, err := workerlivekit.RefreshRunningJobsForReload([]workerlivekit.RunningJobInfo{{Token: "token-a"}}, "", time.Unix(1700001000, 0))
	if err == nil {
		t.Fatal("RefreshRunningJobsForReload(empty secret) error = nil, want error")
	}
	if got := err.Error(); got != "api_secret is required to reload jobs" {
		t.Fatalf("RefreshRunningJobsForReload(empty secret) error = %q, want api_secret required", got)
	}
}

func TestRefreshRunningJobsForReloadRefreshesInOrder(t *testing.T) {
	tokenA, err := auth.NewAccessToken("api-key", "api-secret").SetIdentity("agent-a").ToJWT()
	if err != nil {
		t.Fatalf("ToJWT(tokenA) error = %v", err)
	}
	tokenB, err := auth.NewAccessToken("api-key", "api-secret").SetIdentity("agent-b").ToJWT()
	if err != nil {
		t.Fatalf("ToJWT(tokenB) error = %v", err)
	}
	now := time.Unix(1700001000, 0)

	jobs, err := workerlivekit.RefreshRunningJobsForReload([]workerlivekit.RunningJobInfo{
		{Job: &lkprotocol.Job{Id: "job-a"}, Token: tokenA},
		{Job: &lkprotocol.Job{Id: "job-b"}, Token: tokenB},
	}, "api-secret", now)
	if err != nil {
		t.Fatalf("RefreshRunningJobsForReload() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("RefreshRunningJobsForReload() len = %d, want 2", len(jobs))
	}
	if jobs[0].Job.GetId() != "job-a" || jobs[1].Job.GetId() != "job-b" {
		t.Fatalf("RefreshRunningJobsForReload() job order = %q, %q; want job-a, job-b", jobs[0].Job.GetId(), jobs[1].Job.GetId())
	}
	for i, job := range jobs {
		claims, err := workerlivekit.TokenClaims(job.Token)
		if err != nil {
			t.Fatalf("TokenClaims(job %d) error = %v", i, err)
		}
		if claims.Identity == "" {
			t.Fatalf("TokenClaims(job %d).Identity = empty, want preserved identity", i)
		}
	}
}
