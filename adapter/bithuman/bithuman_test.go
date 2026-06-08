package bithuman

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestBithumanPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.bithuman" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.bithuman", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.bithuman" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.bithuman", PluginPackage)
	}
}

func TestNewBithumanAvatarUsesReferenceDefaults(t *testing.T) {
	avatar := NewBithumanAvatar("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.Provider() != "bithuman" {
		t.Fatalf("Provider() = %q, want bithuman", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "bithuman-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestBithumanAvatarUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewBithumanAvatar("test-key")

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
