package cavos

import (
	"testing"
)

func TestCavosPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.cavos" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.cavos", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatalf("PluginVersion = %q, want non-empty project release version", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.cavos" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.cavos", PluginPackage)
	}
}
