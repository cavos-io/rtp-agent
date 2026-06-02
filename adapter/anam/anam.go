package anam

import (
	"context"
	"errors"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName                  = "anam"
	defaultAvatarAgentIdentity    = "anam-avatar-agent"
	defaultAvatarAgentName        = "anam-avatar-agent"
	defaultInitialAnamAvatarState = agent.AvatarStateIdle
)

type AnamAvatar struct {
	apiKey         string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAnamAvatar(apiKey string) *AnamAvatar {
	if apiKey == "" {
		apiKey = os.Getenv("ANAM_API_KEY")
	}
	return &AnamAvatar{
		apiKey:         apiKey,
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAnamAvatarState,
	}
}

func (a *AnamAvatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("ANAM_API_KEY must be set by arguments or environment variables")
	}
	a.started = true
	return nil
}

func (a *AnamAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}
