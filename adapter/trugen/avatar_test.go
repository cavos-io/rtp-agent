package trugen

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestAvatarConstructorContract(t *testing.T) {
	var _ agent.AvatarProvider = (*Avatar)(nil)
	avatar := NewAvatar("key")
	if avatar.apiKey != "key" || avatar.avatarID != defaultAvatarID || avatar.state != agent.AvatarStateIdle {
		t.Fatalf("NewAvatar() did not apply provider defaults")
	}
}
