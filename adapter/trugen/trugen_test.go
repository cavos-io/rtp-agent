package trugen

import "testing"

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
