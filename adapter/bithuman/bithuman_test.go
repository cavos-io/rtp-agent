package bithuman

import "testing"

func TestBithumanPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.bithuman" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.bithuman", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.bithuman" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.bithuman", PluginPackage)
	}
}
