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

type TavusAvatar struct {
	apiKey         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewTavusAvatar(apiKey string) *TavusAvatar {
	return &TavusAvatar{
		apiKey:         resolveTavusAPIKey(apiKey),
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *TavusAvatar) Start(ctx context.Context) error {
	return nil
}

func (a *TavusAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *TavusAvatar) Provider() string {
	return providerName
}

func (a *TavusAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}
