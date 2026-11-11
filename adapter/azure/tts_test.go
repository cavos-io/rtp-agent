package azure

import (
	"strings"
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

func TestTTSSSMLRemovesBoundarySilence(t *testing.T) {
	ssml := buildAzureTTSSSML(&TTS{voice: "id-ID-ArdiNeural"}, "id-ID", "Baik, saya akan melanjutkan ceritanya.")
	want := `<voice name="id-ID-ArdiNeural"><mstts:silence type="Leading-exact" value="0ms"/><mstts:silence type="Tailing-exact" value="0ms"/>Baik, saya akan melanjutkan ceritanya.</voice>`
	if !strings.Contains(ssml, want) {
		t.Fatalf("SSML lacks sentence boundary silence controls: %s", ssml)
	}
}
