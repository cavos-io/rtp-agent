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

type SimliAvatar struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewSimliAvatar(apiKey string) *SimliAvatar {
	return &SimliAvatar{
		apiKey:         apiKey,
		apiURL:         defaultAPIURL,
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *SimliAvatar) Start(ctx context.Context) error {
	return nil
}

func (a *SimliAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *SimliAvatar) Provider() string {
	return providerName
}

func (a *SimliAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}
