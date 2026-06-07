package lemonslice

import "testing"

func TestLemonSlicePluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.lemonslice" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.lemonslice", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.lemonslice" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.lemonslice", PluginPackage)
	}
}
