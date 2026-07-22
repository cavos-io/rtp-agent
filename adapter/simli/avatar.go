package simli

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "simli"
	defaultAPIURL              = "https://api.simli.ai"
	defaultAvatarAgentIdentity = "simli-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
)

type Avatar struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	return &Avatar{
		apiKey:         apiKey,
		apiURL:         defaultAPIURL,
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
type SimliAvatar = Avatar

// Deprecated: use NewAvatar.
func NewSimliAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
