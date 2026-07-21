package slng

import (
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestTTSConstructorContract(t *testing.T) {
	var _ coretts.TTS = (*TTS)(nil)
	provider := NewTTS("key", WithTTSModel("custom"), WithTTSVoice("voice"))
	if provider.apiKey != "key" || provider.model != "custom" || provider.voice != "voice" {
		t.Fatalf("NewTTS() options not applied: key=%q model=%q voice=%q", provider.apiKey, provider.model, provider.voice)
	}
}
