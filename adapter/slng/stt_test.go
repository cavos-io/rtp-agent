package slng

import (
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTConstructorContract(t *testing.T) {
	var _ corestt.STT = (*STT)(nil)
	provider := NewSTT("key", WithSTTModel("custom"), WithSTTLanguage("id"))
	if provider.apiKey != "key" || provider.model != "custom" || provider.language != "id" {
		t.Fatalf("NewSTT() options not applied: key=%q model=%q language=%q", provider.apiKey, provider.model, provider.language)
	}
}
