package lemonslice

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestLemonSlicePluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.lemonslice" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.lemonslice", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.lemonslice" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.lemonslice", PluginPackage)
	}
}

func TestNewLemonsliceAvatarUsesReferenceDefaults(t *testing.T) {
	avatar := NewLemonsliceAvatar("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.apiURL != "https://lemonslice.com/api/liveai/sessions" {
		t.Fatalf("apiURL = %q, want reference API URL", avatar.apiURL)
	}
	if avatar.Provider() != "lemonslice" {
		t.Fatalf("Provider() = %q, want lemonslice", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "lemonslice-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewLemonsliceAvatarUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("LEMONSLICE_API_KEY", "env-key")

	avatar := NewLemonsliceAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}

	explicit := NewLemonsliceAvatar("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", explicit.apiKey)
	}
}

func TestLemonsliceAvatarUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewLemonsliceAvatar("test-key")

	if err := avatar.UpdateState(agent.AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState speaking error = %v", err)
	}
	if avatar.state != agent.AvatarStateSpeaking {
		t.Fatalf("state = %q, want speaking", avatar.state)
	}
	if err := avatar.UpdateState(agent.AvatarStateIdle); err != nil {
		t.Fatalf("UpdateState idle error = %v", err)
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}
