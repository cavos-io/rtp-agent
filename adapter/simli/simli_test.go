package simli

import "testing"

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
