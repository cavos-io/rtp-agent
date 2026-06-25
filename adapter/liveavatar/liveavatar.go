package liveavatar

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "liveavatar"
	liveAvatarAPIKeyEnv        = "LIVEAVATAR_API_KEY"
	liveAvatarAvatarIDEnv      = "LIVEAVATAR_AVATAR_ID"
	defaultAvatarAgentIdentity = "liveavatar-avatar-agent"
	defaultAvatarAgentName     = "liveavatar-avatar-agent"
)

type LiveAvatar struct {
	apiKey         string
	avatarID       string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
}

func NewLiveAvatar(apiKey string) *LiveAvatar {
	if apiKey == "" {
		apiKey = os.Getenv(liveAvatarAPIKeyEnv)
	}
	return &LiveAvatar{
		apiKey:         apiKey,
		avatarID:       os.Getenv(liveAvatarAvatarIDEnv),
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          agent.AvatarStateIdle,
	}
}

func (a *LiveAvatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("LIVEAVATAR_API_KEY must be set")
	}
	if a.avatarID == "" {
		return errors.New("LIVEAVATAR_AVATAR_ID must be set")
	}
	fmt.Println("LiveAvatar started.")
	return nil
}

func (a *LiveAvatar) Provider() string {
	return providerName
}

func (a *LiveAvatar) AvatarIdentity() string {
	return a.avatarIdentity
}

func (a *LiveAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	fmt.Printf("LiveAvatar state updated to: %s\n", state)
	return nil
}
