package ultravox

import (
	"context"
	"strings"
	"testing"
)

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

func TestNewUltravoxTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "env-key")

	provider := NewUltravoxTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewUltravoxTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestUltravoxTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("ULTRAVOX_API_KEY", "")
	provider := NewUltravoxTTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "ULTRAVOX_API_KEY") {
		t.Fatalf("Synthesize error = %q, want ULTRAVOX_API_KEY guidance", err)
	}
}
