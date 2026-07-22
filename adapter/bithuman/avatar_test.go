package bithuman

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestAvatarConstructorContract(t *testing.T) {
	var _ agent.AvatarProvider = (*Avatar)(nil)
	avatar := NewAvatar("key")
	if avatar.apiKey != "key" || avatar.AvatarIdentity() != defaultAvatarAgentIdentity || avatar.state != agent.AvatarStateIdle {
		t.Fatalf("NewAvatar() did not apply identity defaults")
	}
}
