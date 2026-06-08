package tavus

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestTavusPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.tavus" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.tavus", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.tavus" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.tavus", PluginPackage)
	}
}

func TestNewTavusAvatarUsesReferenceDefaults(t *testing.T) {
	avatar := NewTavusAvatar("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.Provider() != "tavus" {
		t.Fatalf("Provider() = %q, want tavus", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "tavus-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestTavusAvatarUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewTavusAvatar("test-key")

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
