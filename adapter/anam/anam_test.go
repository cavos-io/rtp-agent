package anam

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestNewAnamAvatarUsesExplicitAPIKeyAndReferenceMetadata(t *testing.T) {
	t.Setenv("ANAM_API_KEY", "env-key")

	avatar := NewAnamAvatar("explicit-key")

	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
	if providerName != "anam" {
		t.Fatalf("providerName = %q, want anam", providerName)
	}
	if avatar.avatarIdentity != "anam-avatar-agent" {
		t.Fatalf("avatarIdentity = %q, want reference identity", avatar.avatarIdentity)
	}
	if avatar.avatarName != "anam-avatar-agent" {
		t.Fatalf("avatarName = %q, want reference name", avatar.avatarName)
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewAnamAvatarFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ANAM_API_KEY", "env-key")

	avatar := NewAnamAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
}

func TestAnamAvatarStartRequiresAPIKey(t *testing.T) {
	t.Setenv("ANAM_API_KEY", "")
	avatar := NewAnamAvatar("")

	err := avatar.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "ANAM_API_KEY must be set") {
		t.Fatalf("Start error = %v, want missing API key error", err)
	}
	if avatar.started {
		t.Fatal("Started() = true, want false after failed start")
	}
}

func TestAnamAvatarStartAndUpdateState(t *testing.T) {
	avatar := NewAnamAvatar("explicit-key")

	if err := avatar.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !avatar.started {
		t.Fatal("Started() = false, want true")
	}

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState returned error: %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
}

func TestNewAnamAvatarUsesReferenceAPIURLAndPersonaConfig(t *testing.T) {
	t.Setenv(anamAPIURLEnv, "")

	persona := PersonaConfig{
		Name:        "Support agent",
		AvatarID:    "avatar-123",
		AvatarModel: "anam-model",
	}

	avatar := NewAnamAvatar("explicit-key", persona)

	if avatar.apiURL != defaultAnamAPIURL {
		t.Fatalf("apiURL = %q, want reference default", avatar.apiURL)
	}
	if avatar.personaConfig != persona {
		t.Fatalf("personaConfig = %#v, want %#v", avatar.personaConfig, persona)
	}
	if avatar.Provider() != "anam" {
		t.Fatalf("Provider() = %q, want anam", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "anam-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
}

func TestNewAnamAvatarUsesEnvironmentAPIURL(t *testing.T) {
	t.Setenv(anamAPIURLEnv, "https://anam.local")

	avatar := NewAnamAvatar("explicit-key", PersonaConfig{Name: "Support", AvatarID: "avatar-123"})

	if avatar.apiURL != "https://anam.local" {
		t.Fatalf("apiURL = %q, want env value", avatar.apiURL)
	}
}

func TestBuildAnamSessionTokenRequestMatchesReferencePayload(t *testing.T) {
	persona := PersonaConfig{
		Name:        "Support agent",
		AvatarID:    "avatar-123",
		AvatarModel: "anam-model",
	}

	endpoint, headers, body, err := buildAnamSessionTokenRequest("api-key", persona, "wss://livekit.example", "livekit-token")
	if err != nil {
		t.Fatalf("buildAnamSessionTokenRequest returned error: %v", err)
	}

	if endpoint != "/v1/auth/session-token" {
		t.Fatalf("endpoint = %q, want reference endpoint", endpoint)
	}
	if headers["Authorization"] != "Bearer api-key" {
		t.Fatalf("Authorization = %q, want bearer API key", headers["Authorization"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", headers["Content-Type"])
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}

	personaConfig := payload["personaConfig"].(map[string]any)
	if personaConfig["type"] != "ephemeral" {
		t.Fatalf("personaConfig.type = %v, want ephemeral", personaConfig["type"])
	}
	if personaConfig["name"] != "Support agent" {
		t.Fatalf("personaConfig.name = %v, want Support agent", personaConfig["name"])
	}
	if personaConfig["avatarId"] != "avatar-123" {
		t.Fatalf("personaConfig.avatarId = %v, want avatar-123", personaConfig["avatarId"])
	}
	if personaConfig["avatarModel"] != "anam-model" {
		t.Fatalf("personaConfig.avatarModel = %v, want anam-model", personaConfig["avatarModel"])
	}
	if personaConfig["llmId"] != "CUSTOMER_CLIENT_V1" {
		t.Fatalf("personaConfig.llmId = %v, want CUSTOMER_CLIENT_V1", personaConfig["llmId"])
	}

	environment := payload["environment"].(map[string]any)
	if environment["livekitUrl"] != "wss://livekit.example" {
		t.Fatalf("environment.livekitUrl = %v, want livekit URL", environment["livekitUrl"])
	}
	if environment["livekitToken"] != "livekit-token" {
		t.Fatalf("environment.livekitToken = %v, want livekit token", environment["livekitToken"])
	}
}
