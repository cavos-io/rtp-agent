package cavos

import "testing"

func TestCavosPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.cavos" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.cavos", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.cavos" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.cavos", PluginPackage)
	}
}
