package nvidia

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

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

func TestNvidiaTTSReferenceDefaultsAndCapabilities(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}
	if provider.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", provider.apiKey)
	}
	if got, want := provider.voice, "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := provider.server, "grpc.nvcf.nvidia.com:443"; got != want {
		t.Fatalf("server = %q, want reference default server %q", got, want)
	}
	if got, want := provider.functionID, "877104f7-e885-42b9-8de8-f6e4c6303969"; got != want {
		t.Fatalf("functionID = %q, want reference function id %q", got, want)
	}
	if got, want := provider.languageCode, "en-US"; got != want {
		t.Fatalf("languageCode = %q, want %q", got, want)
	}
	if !provider.useSSL {
		t.Fatal("useSSL = false, want reference default true")
	}
	if got, want := provider.Label(), "nvidia.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if got, want := tts.Model(provider), "Magpie-Multilingual.EN-US.Leo"; got != want {
		t.Fatalf("tts.Model() = %q, want %q", got, want)
	}
	if got, want := tts.Provider(provider), "nvidia"; got != want {
		t.Fatalf("tts.Provider() = %q, want %q", got, want)
	}
	if got, want := provider.SampleRate(), 16000; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := provider.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}
	if caps := provider.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
}

func TestNvidiaTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "env-secret")

	provider, err := NewNvidiaTTS("", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if got, want := provider.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestNvidiaTTSRequiresAPIKeyWhenUsingSSL(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	_, err := NewNvidiaTTS("", "")

	if err == nil || !strings.Contains(err.Error(), "nvidia api key") {
		t.Fatalf("NewNvidiaTTS error = %v, want missing key error", err)
	}
}

func TestNvidiaTTSAllowsLocalRivaWithoutAPIKey(t *testing.T) {
	t.Setenv("NVIDIA_API_KEY", "")

	provider, err := NewNvidiaTTS("", "", WithNvidiaTTSUseSSL(false))
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v, want local Riva config without key", err)
	}

	if provider.useSSL {
		t.Fatal("useSSL = true, want false")
	}
	if provider.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty local key", provider.apiKey)
	}
}

func TestNvidiaTTSReportsUnsupportedRivaCalls(t *testing.T) {
	provider, err := NewNvidiaTTS("secret", "")
	if err != nil {
		t.Fatalf("NewNvidiaTTS error = %v", err)
	}

	if _, err := provider.Synthesize(context.Background(), "hello"); err == nil || !strings.Contains(err.Error(), "riva tts synthesis is not implemented") {
		t.Fatalf("Synthesize() error = %v, want explicit unsupported synthesis error", err)
	}
	if _, err := provider.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "riva tts streaming is not implemented") {
		t.Fatalf("Stream() error = %v, want explicit unsupported stream error", err)
	}
}
