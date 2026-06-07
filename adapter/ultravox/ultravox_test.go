package ultravox

import "testing"

func TestUltravoxPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.ultravox" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.ultravox", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.ultravox" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.ultravox", PluginPackage)
	}
}
