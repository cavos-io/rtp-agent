package bithuman

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "bithuman"
	defaultAvatarAgentIdentity = "bithuman-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
	bithumanAPISecretEnv       = "BITHUMAN_API_SECRET"
)

type Avatar struct {
	apiKey         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	if apiKey == "" {
		apiKey = os.Getenv(bithumanAPISecretEnv)
	}
	return &Avatar{
		apiKey:         apiKey,
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
type BithumanAvatar = Avatar

// Deprecated: use NewAvatar.
func NewBithumanAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
