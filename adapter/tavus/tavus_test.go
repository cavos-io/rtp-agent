package tavus

import "testing"

func TestTavusPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.tavus" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.tavus", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.tavus" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.tavus", PluginPackage)
	}
}
