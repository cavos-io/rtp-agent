package spitch

import (
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTConstructorContract(t *testing.T) {
	var _ corestt.STT = (*STT)(nil)
	provider := NewSTT("key")
	if provider.apiKey != "key" || provider.language != "en" {
		t.Fatalf("NewSTT() defaults = key %q, language %q", provider.apiKey, provider.language)
	}
}
