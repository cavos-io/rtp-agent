package bey

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestNewBeyAvatarUsesReferenceDefaultsAndExplicitAPIKey(t *testing.T) {
	t.Setenv(beyAPIKeyEnv, "env-key")
	t.Setenv(beyAPIURLEnv, "")

	avatar, err := NewBeyAvatar("explicit-key")
	if err != nil {
		t.Fatalf("NewBeyAvatar error = %v", err)
	}

	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
	if avatar.apiURL != defaultBeyAPIURL {
		t.Fatalf("apiURL = %q, want reference default", avatar.apiURL)
	}
	if avatar.avatarID != defaultBeyAvatarID {
		t.Fatalf("avatarID = %q, want reference stock avatar", avatar.avatarID)
	}
	if avatar.avatarIdentity != defaultBeyAvatarAgentIdentity {
		t.Fatalf("avatarIdentity = %q, want reference identity", avatar.avatarIdentity)
	}
	if avatar.avatarName != defaultBeyAvatarAgentName {
		t.Fatalf("avatarName = %q, want reference name", avatar.avatarName)
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
	if avatar.Provider() != "bey" {
		t.Fatalf("Provider = %q, want bey", avatar.Provider())
	}
	if avatar.AvatarIdentity() != defaultBeyAvatarAgentIdentity {
		t.Fatalf("AvatarIdentity = %q, want reference identity", avatar.AvatarIdentity())
	}
}

func TestNewBeyAvatarFallsBackToEnvironment(t *testing.T) {
	t.Setenv(beyAPIKeyEnv, "env-key")
	t.Setenv(beyAPIURLEnv, "https://bey.local")

	avatar, err := NewBeyAvatar("")
	if err != nil {
		t.Fatalf("NewBeyAvatar error = %v", err)
	}

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
	if avatar.apiURL != "https://bey.local" {
		t.Fatalf("apiURL = %q, want env API URL", avatar.apiURL)
	}
}

func TestNewBeyAvatarRequiresAPIKey(t *testing.T) {
	t.Setenv(beyAPIKeyEnv, "")

	_, err := NewBeyAvatar("")

	if err == nil || !strings.Contains(err.Error(), "BEY_API_KEY") {
		t.Fatalf("NewBeyAvatar error = %v, want API key error", err)
	}
}

func TestBeyAvatarStartAndUpdateState(t *testing.T) {
	avatar, err := NewBeyAvatar("test-key")
	if err != nil {
		t.Fatalf("NewBeyAvatar error = %v", err)
	}

	if err := avatar.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if !avatar.started {
		t.Fatal("started = false, want true")
	}

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState error = %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
}

func TestBuildBeySessionRequestMatchesReference(t *testing.T) {
	avatar, err := NewBeyAvatar("test-key")
	if err != nil {
		t.Fatalf("NewBeyAvatar error = %v", err)
	}

	endpoint, headers, body, err := buildBeySessionRequest(avatar, "wss://livekit.example", "livekit-token")
	if err != nil {
		t.Fatalf("buildBeySessionRequest error = %v", err)
	}

	if endpoint != defaultBeyAPIURL+"/v1/session" {
		t.Fatalf("endpoint = %q, want reference session endpoint", endpoint)
	}
	if headers.Get("x-api-key") != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", headers.Get("x-api-key"))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if payload["avatar_id"] != defaultBeyAvatarID {
		t.Fatalf("avatar_id = %#v, want reference avatar", payload["avatar_id"])
	}
	if payload["livekit_url"] != "wss://livekit.example" {
		t.Fatalf("livekit_url = %#v, want livekit URL", payload["livekit_url"])
	}
	if payload["livekit_token"] != "livekit-token" {
		t.Fatalf("livekit_token = %#v, want livekit token", payload["livekit_token"])
	}
}
