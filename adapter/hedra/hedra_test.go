package hedra

import "testing"

func TestHedraPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.hedra" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.hedra", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.hedra" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.hedra", PluginPackage)
	}
}
