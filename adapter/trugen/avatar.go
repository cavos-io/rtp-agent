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

type TrugenAvatar struct {
	apiKey         string
	avatarID       string
	avatarIdentity string
	state          agent.AvatarState
}

func NewTrugenAvatar(apiKey string) *TrugenAvatar {
	return &TrugenAvatar{
		apiKey:         apiKey,
		avatarID:       defaultAvatarID,
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *TrugenAvatar) Start(ctx context.Context) error {
	return nil
}

func (a *TrugenAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *TrugenAvatar) Provider() string {
	return providerName
}

func (a *TrugenAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}
