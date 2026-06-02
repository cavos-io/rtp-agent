package anam

import (
	"context"
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
