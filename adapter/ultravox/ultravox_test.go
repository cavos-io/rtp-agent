package ultravox

import (
	"bytes"
	"context"
	"io"
	"net/http"
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

func TestUltravoxTTSChunkedStreamEmitsReferenceFinalMarker(t *testing.T) {
	stream := &ultravoxTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04}))},
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first Next error = %v", err)
	}
	if audio == nil || audio.Frame == nil || audio.IsFinal {
		t.Fatalf("first Next = %+v, want audio frame", audio)
	}

	final, err := stream.Next()
	if err != nil {
		t.Fatalf("second Next error = %v, want final marker", err)
	}
	if final == nil || !final.IsFinal || final.Frame != nil {
		t.Fatalf("second Next = %+v, want boundary-only final marker", final)
	}

	audio, err = stream.Next()
	if err != io.EOF || audio != nil {
		t.Fatalf("Next after final marker = (%+v, %v), want EOF", audio, err)
	}
}
