package avatario

import (
	"context"
	"errors"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName                = "avatario"
	defaultAvatarAgentIdentity  = "avatario-avatar-agent"
	defaultAvatarAgentName      = "avatario-avatar-agent"
	defaultInitialAvatarioState = agent.AvatarStateIdle
	avatarioAPIKeyEnv           = "AVATARIO_API_KEY"
	avatarioAvatarIDEnv         = "AVATARIO_AVATAR_ID"
)

type Avatar struct {
	apiKey         string
	avatarID       string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAvatar(apiKey string) *Avatar {
	if apiKey == "" {
		apiKey = os.Getenv(avatarioAPIKeyEnv)
	}
	return &Avatar{
		apiKey:         apiKey,
		avatarID:       os.Getenv(avatarioAvatarIDEnv),
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAvatarioState,
	}
}

func (a *Avatar) Provider() string {
	return providerName
}

func (a *Avatar) Start(ctx context.Context) error {
	if a.avatarID == "" {
		return errors.New("AVATARIO_AVATAR_ID must be set")
	}
	if a.apiKey == "" {
		return errors.New("AVATARIO_API_KEY must be set")
	}
	a.started = true
	return nil
}

func (a *Avatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

// Deprecated: use Avatar.
type AvatarioAvatar = Avatar

// Deprecated: use NewAvatar.
func NewAvatarioAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
