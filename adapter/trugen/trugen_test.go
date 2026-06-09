package trugen

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestTrugenPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.trugen" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.trugen", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.trugen" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.trugen", PluginPackage)
	}
}

func TestNewTrugenAvatarUsesReferenceDefaults(t *testing.T) {
	avatar := NewTrugenAvatar("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.avatarID != "665a1170" {
		t.Fatalf("avatarID = %q, want reference default avatar ID", avatar.avatarID)
	}
	if avatar.Provider() != "trugen" {
		t.Fatalf("Provider() = %q, want trugen", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "trugen-avatar" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewTrugenAvatarUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("TRUGEN_API_KEY", "env-key")

	avatar := NewTrugenAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}

	explicit := NewTrugenAvatar("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", explicit.apiKey)
	}
}

func TestTrugenAvatarUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewTrugenAvatar("test-key")

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
