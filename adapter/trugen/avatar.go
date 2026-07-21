package trugen

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "trugen"
	defaultAvatarAgentIdentity = "trugen-avatar"
	defaultAvatarID            = "665a1170"
	defaultInitialAvatarState  = agent.AvatarStateIdle
)

type Avatar struct {
	apiKey         string
	avatarID       string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	return &Avatar{
		apiKey:         resolveTrugenAPIKey(apiKey),
		avatarID:       defaultAvatarID,
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
type TrugenAvatar = Avatar

// Deprecated: use NewAvatar.
func NewTrugenAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
