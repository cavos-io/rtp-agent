package bithuman

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "bithuman"
	defaultAvatarAgentIdentity = "bithuman-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
)

type BithumanAvatar struct {
	apiKey         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewBithumanAvatar(apiKey string) *BithumanAvatar {
	return &BithumanAvatar{
		apiKey:         apiKey,
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *BithumanAvatar) Start(ctx context.Context) error {
	return nil
}

func (a *BithumanAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *BithumanAvatar) Provider() string {
	return providerName
}

func (a *BithumanAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}
