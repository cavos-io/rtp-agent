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

type AvatarioAvatar struct {
	apiKey         string
	avatarID       string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAvatarioAvatar(apiKey string) *AvatarioAvatar {
	if apiKey == "" {
		apiKey = os.Getenv(avatarioAPIKeyEnv)
	}
	return &AvatarioAvatar{
		apiKey:         apiKey,
		avatarID:       os.Getenv(avatarioAvatarIDEnv),
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAvatarioState,
	}
}

func (a *AvatarioAvatar) Provider() string {
	return providerName
}

func (a *AvatarioAvatar) Start(ctx context.Context) error {
	if a.avatarID == "" {
		return errors.New("AVATARIO_AVATAR_ID must be set")
	}
	if a.apiKey == "" {
		return errors.New("AVATARIO_API_KEY must be set")
	}
	a.started = true
	return nil
}

func (a *AvatarioAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}
