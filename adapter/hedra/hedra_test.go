package hedra

import (
	"context"
	"strings"
	"testing"
)

func TestHedraPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.hedra" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.hedra", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.hedra" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.hedra", PluginPackage)
	}
}

func TestHedraAvatarStartReportsDisabledReferenceService(t *testing.T) {
	avatar := NewHedraAvatar("test-key")

	err := avatar.Start(context.Background())

	if err == nil {
		t.Fatal("Start() error = nil, want disabled service error")
	}
	if !strings.Contains(err.Error(), "hedra realtime avatar service has been disabled") {
		t.Fatalf("Start() error = %q, want disabled reference service message", err)
	}
}
