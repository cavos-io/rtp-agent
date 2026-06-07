package minimal

import "testing"

func TestMinimalPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.minimal" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.minimal", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.minimal" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.minimal", PluginPackage)
	}
}
