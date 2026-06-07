package nvidia

import "testing"

func TestNvidiaPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.nvidia", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.nvidia" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.nvidia", PluginPackage)
	}
}
