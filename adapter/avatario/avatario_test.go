package avatario

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestNewAvatarioAvatarUsesReferenceDefaultsAndEnvAvatarID(t *testing.T) {
	t.Setenv(avatarioAPIKeyEnv, "env-key")
	t.Setenv(avatarioAvatarIDEnv, "avatar-123")

	avatar := NewAvatarioAvatar("explicit-key")

	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
	if providerName != "avatario" {
		t.Fatalf("providerName = %q, want avatario", providerName)
	}
	if avatar.avatarID != "avatar-123" {
		t.Fatalf("avatarID = %q, want env avatar ID", avatar.avatarID)
	}
	if avatar.avatarIdentity != "avatario-avatar-agent" {
		t.Fatalf("avatarIdentity = %q, want reference identity", avatar.avatarIdentity)
	}
	if avatar.avatarName != "avatario-avatar-agent" {
		t.Fatalf("avatarName = %q, want reference name", avatar.avatarName)
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewAvatarioAvatarFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv(avatarioAPIKeyEnv, "env-key")
	t.Setenv(avatarioAvatarIDEnv, "avatar-123")

	avatar := NewAvatarioAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
}

func TestAvatarioAvatarStartRequiresAvatarID(t *testing.T) {
	t.Setenv(avatarioAPIKeyEnv, "env-key")
	t.Setenv(avatarioAvatarIDEnv, "")
	avatar := NewAvatarioAvatar("")

	err := avatar.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "AVATARIO_AVATAR_ID must be set") {
		t.Fatalf("Start error = %v, want missing avatar ID error", err)
	}
	if avatar.started {
		t.Fatal("started = true, want false after failed start")
	}
}

func TestAvatarioAvatarStartRequiresAPIKey(t *testing.T) {
	t.Setenv(avatarioAPIKeyEnv, "")
	t.Setenv(avatarioAvatarIDEnv, "avatar-123")
	avatar := NewAvatarioAvatar("")

	err := avatar.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "AVATARIO_API_KEY must be set") {
		t.Fatalf("Start error = %v, want missing API key error", err)
	}
	if avatar.started {
		t.Fatal("started = true, want false after failed start")
	}
}

func TestAvatarioAvatarStartAndUpdateState(t *testing.T) {
	t.Setenv(avatarioAvatarIDEnv, "avatar-123")
	avatar := NewAvatarioAvatar("explicit-key")

	if err := avatar.Start(context.Background()); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if !avatar.started {
		t.Fatal("started = false, want true")
	}

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState returned error: %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
}
