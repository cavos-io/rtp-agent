package nvidia

import (
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestTTSConstructorContract(t *testing.T) {
	var _ coretts.TTS = (*TTS)(nil)
	t.Setenv(nvidiaAPIKeyEnv, "")

	if _, err := NewTTS("", ""); err == nil {
		t.Fatal("NewTTS() error = nil, want missing API key error")
	}
	provider, err := NewTTS("", "voice", WithNvidiaTTSUseSSL(false))
	if err != nil {
		t.Fatalf("NewTTS() local transport error = %v", err)
	}
	if provider.voice != "voice" || provider.useSSL {
		t.Fatalf("NewTTS() options = voice %q, useSSL %v", provider.voice, provider.useSSL)
	}
}
