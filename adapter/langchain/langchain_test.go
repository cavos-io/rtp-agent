package langchain

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

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

func TestLangchainLLMMetadataMatchesReference(t *testing.T) {
	provider := NewLangchainLLM("test-key", "")

	if got := llm.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := llm.Provider(provider); got != "LangChain" {
		t.Fatalf("provider metadata = %q, want LangChain", got)
	}
}
