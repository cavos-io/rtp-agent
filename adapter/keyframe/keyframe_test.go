package keyframe

import "testing"

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
