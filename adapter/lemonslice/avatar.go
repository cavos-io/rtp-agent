package lemonslice

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "lemonslice"
	defaultAPIURL              = "https://lemonslice.com/api/liveai/sessions"
	defaultAvatarAgentIdentity = "lemonslice-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
)

type LemonsliceAvatar struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewLemonsliceAvatar(apiKey string) *LemonsliceAvatar {
	return &LemonsliceAvatar{
		apiKey:         apiKey,
		apiURL:         defaultAPIURL,
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *LemonsliceAvatar) Start(ctx context.Context) error {
	return nil
}

func (a *LemonsliceAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *LemonsliceAvatar) Provider() string {
	return providerName
}

func (a *LemonsliceAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}
