package keyframe

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestKeyframePluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.keyframe" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.keyframe", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.keyframe" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.keyframe", PluginPackage)
	}
}

func TestNewKeyframeAgentUsesReferenceDefaults(t *testing.T) {
	avatar := NewKeyframeAgent("test-key")

	if avatar.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit API key", avatar.apiKey)
	}
	if avatar.apiURL != "https://api.keyframelabs.com" {
		t.Fatalf("apiURL = %q, want reference API URL", avatar.apiURL)
	}
	if avatar.Provider() != "keyframe" {
		t.Fatalf("Provider() = %q, want keyframe", avatar.Provider())
	}
	if avatar.AvatarIdentity() != "keyframe-avatar-agent" {
		t.Fatalf("AvatarIdentity() = %q, want reference identity", avatar.AvatarIdentity())
	}
	if avatar.state != agent.AvatarStateIdle {
		t.Fatalf("state = %q, want idle", avatar.state)
	}
}

func TestNewKeyframeAgentUsesEnvironmentConfig(t *testing.T) {
	t.Setenv("KEYFRAME_API_KEY", "env-key")
	t.Setenv("KEYFRAME_API_URL", "https://keyframe.example")

	avatar := NewKeyframeAgent("")

	if avatar.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", avatar.apiKey)
	}
	if avatar.apiURL != "https://keyframe.example" {
		t.Fatalf("apiURL = %q, want env API URL", avatar.apiURL)
	}

	explicit := NewKeyframeAgent("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", explicit.apiKey)
	}
}

func TestKeyframeAgentUpdateStateRecordsReferenceState(t *testing.T) {
	avatar := NewKeyframeAgent("test-key")

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
