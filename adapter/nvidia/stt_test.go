package nvidia

import (
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTConstructorContract(t *testing.T) {
	var _ corestt.STT = (*STT)(nil)
	t.Setenv(nvidiaAPIKeyEnv, "")

	if _, err := NewSTT("", ""); err == nil {
		t.Fatal("NewSTT() error = nil, want missing API key error")
	}
	provider, err := NewSTT("", "", WithNvidiaSTTUseSSL(false))
	if err != nil {
		t.Fatalf("NewSTT() local transport error = %v", err)
	}
	if provider.model != defaultNvidiaSTTModel || provider.useSSL {
		t.Fatalf("NewSTT() defaults = model %q, useSSL %v", provider.model, provider.useSSL)
	}
}
