package azure

import (
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestTTSConstructorContract(t *testing.T) {
	var _ coretts.TTS = (*TTS)(nil)
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")
	t.Setenv(azureSpeechAuthTokenEnv, "")
	t.Setenv(azureSpeechHostEnv, "")
	t.Setenv(azureSpeechEndpointEnv, "")

	if _, err := NewTTS("", "", ""); err == nil {
		t.Fatal("NewTTS() error = nil, want missing configuration error")
	}
}
