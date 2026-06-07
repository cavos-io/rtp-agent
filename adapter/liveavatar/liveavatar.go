package liveavatar

import (
	"context"
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
