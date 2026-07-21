package azure

import (
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
)

func TestSTTConstructorContract(t *testing.T) {
	var _ corestt.STT = (*STT)(nil)
	t.Setenv(azureSpeechKeyEnv, "")
	t.Setenv(azureSpeechRegionEnv, "")
	t.Setenv(azureSpeechAuthTokenEnv, "")
	t.Setenv(azureSpeechHostEnv, "")
	t.Setenv(azureSpeechEndpointEnv, "")

	if _, err := NewSTT("", ""); err == nil {
		t.Fatal("NewSTT() error = nil, want missing configuration error")
	}
}
