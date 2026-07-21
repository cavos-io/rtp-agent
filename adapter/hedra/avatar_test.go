package hedra

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestAvatarConstructorContract(t *testing.T) {
	var _ agent.AvatarProvider = (*Avatar)(nil)
	avatar := NewAvatar("key")
	if avatar.apiKey != "key" {
		t.Fatalf("NewAvatar() API key = %q, want key", avatar.apiKey)
	}
	if err := avatar.Start(context.Background()); err == nil {
		t.Fatal("Start() error = nil, want disabled-provider error")
	}
}
