package tavus

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "tavus"
	defaultAvatarAgentIdentity = "tavus-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
)

type Avatar struct {
	apiKey         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	return &Avatar{
		apiKey:         resolveTavusAPIKey(apiKey),
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *Avatar) Start(ctx context.Context) error {
	return nil
}

func (a *Avatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *Avatar) Provider() string {
	return providerName
}

func (a *Avatar) AvatarIdentity() string {
	return a.avatarIdentity
}

// Deprecated: use Avatar.
type TavusAvatar = Avatar

// Deprecated: use NewAvatar.
func NewTavusAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
