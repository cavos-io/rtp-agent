package simli

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestSimliPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.simli" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.simli", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.simli" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.simli", PluginPackage)
	}
}

func TestNewSimliAvatarUsesReferenceDefaults(t *testing.T) {
	avatar := NewSimliAvatar("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.apiURL != "https://api.simli.ai" {
		t.Fatalf("apiURL = %q, want reference API URL", avatar.apiURL)
	}
	if avatar.Provider() != "simli" {
		t.Fatalf("Provider() = %q, want simli", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "simli-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestSimliAvatarUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewSimliAvatar("test-key")

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
