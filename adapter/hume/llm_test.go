package hume

import (
	"context"
	"strings"
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestHumePluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.hume" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.hume", PluginTitle)
	}
	if PluginVersion == "" {
		t.Fatal("PluginVersion is empty")
	}
	if PluginPackage != "rtp-agent.plugins.hume" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.hume", PluginPackage)
	}
}

func TestHumePackageExposesReferenceTTSCapability(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	if got := provider.Label(); got != "hume.TTS" {
		t.Fatalf("Label() = %q, want hume.TTS", got)
	}
	if got := coretts.Provider(provider); got != "Hume" {
		t.Fatalf("provider metadata = %q, want Hume", got)
	}
	if got := coretts.Model(provider); got != "Octave" {
		t.Fatalf("model metadata = %q, want Octave", got)
	}
	if got := provider.SampleRate(); got != 48000 {
		t.Fatalf("SampleRate() = %d, want 48000", got)
	}
	if got := provider.NumChannels(); got != 1 {
		t.Fatalf("NumChannels() = %d, want mono", got)
	}

	caps := provider.Capabilities()
	if caps.Streaming {
		t.Fatalf("Capabilities().Streaming = true, want false for reference chunked synthesis")
	}
	if caps.AlignedTranscript {
		t.Fatalf("Capabilities().AlignedTranscript = true, want false")
	}
}

func TestHumeTTSRejectsInputStreamingSurface(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	stream, err := provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream() returned nil error, want unsupported streaming error")
	}
	if stream != nil {
		t.Fatalf("Stream() returned stream %T, want nil", stream)
	}
	if !strings.Contains(err.Error(), "streaming tts not natively supported") {
		t.Fatalf("Stream() error = %q, want unsupported streaming context", err.Error())
	}
}
