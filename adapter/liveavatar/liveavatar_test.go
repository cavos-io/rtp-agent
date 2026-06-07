package liveavatar

import (
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestLiveAvatarPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.liveavatar" {
		t.Fatalf("plugin title = %q, want rtp-agent.plugins.liveavatar", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("plugin version = %q, want reference version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.liveavatar" {
		t.Fatalf("plugin package = %q, want rtp-agent.plugins.liveavatar", PluginPackage)
	}
	if !strings.HasPrefix(PluginPackage, "rtp-agent.plugins.") {
		t.Fatalf("plugin package = %q, want rtp-agent namespace", PluginPackage)
	}
}

func TestNewLiveAvatarUsesReferenceDefaultsAndEnv(t *testing.T) {
	t.Setenv(liveAvatarAPIKeyEnv, "env-key")
	t.Setenv(liveAvatarAvatarIDEnv, "avatar-123")

	avatar := NewLiveAvatar("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
	if avatar.avatarID != "avatar-123" {
		t.Fatalf("avatarID = %q, want env avatar ID", avatar.avatarID)
	}
	if avatar.avatarIdentity != "liveavatar-avatar-agent" {
		t.Fatalf("avatarIdentity = %q, want reference identity", avatar.avatarIdentity)
	}
	if avatar.avatarName != "liveavatar-avatar-agent" {
		t.Fatalf("avatarName = %q, want reference name", avatar.avatarName)
	}
	if avatar.Provider() != "liveavatar" {
		t.Fatalf("Provider() = %q, want liveavatar", avatar.Provider())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewLiveAvatarPrefersExplicitAPIKey(t *testing.T) {
	t.Setenv(liveAvatarAPIKeyEnv, "env-key")

	avatar := NewLiveAvatar("explicit-key")

	if avatar.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", avatar.apiKey)
	}
}
