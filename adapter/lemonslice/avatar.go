package lemonslice

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "lemonslice"
	defaultAPIURL              = "https://lemonslice.com/api/liveai/sessions"
	defaultAvatarAgentIdentity = "lemonslice-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
	lemonSliceAPIKeyEnv        = "LEMONSLICE_API_KEY"
)

type Avatar struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	if apiKey == "" {
		apiKey = os.Getenv(lemonSliceAPIKeyEnv)
	}
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
type LemonsliceAvatar = Avatar

// Deprecated: use NewAvatar.
func NewLemonsliceAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
