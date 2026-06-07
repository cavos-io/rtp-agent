package langchain

import "testing"

func TestLangchainPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.langchain" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.langchain", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.langchain" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.langchain", PluginPackage)
	}
}
