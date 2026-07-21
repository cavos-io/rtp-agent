package avatartalk

import (
	"context"
	"errors"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName                  = "avatartalk"
	defaultAvatarAgentIdentity    = "avatartalk-agent"
	defaultAvatarAgentName        = "avatartalk-agent"
	defaultAvatarName             = "japanese_man"
	defaultAvatarEmotion          = "expressive"
	defaultInitialAvatarTalkState = agent.AvatarStateIdle
	avatarTalkAPIKeyEnv           = "AVATARTALK_API_KEY"
	avatarTalkAvatarEnv           = "AVATARTALK_AVATAR"
	avatarTalkEmotionEnv          = "AVATARTALK_EMOTION"
)

type Avatar struct {
	apiKey         string
	avatar         string
	emotion        string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAvatar(apiKey string) *Avatar {
	if apiKey == "" {
		apiKey = os.Getenv(avatarTalkAPIKeyEnv)
	}
	avatar := os.Getenv(avatarTalkAvatarEnv)
	if avatar == "" {
		avatar = defaultAvatarName
	}
	emotion := os.Getenv(avatarTalkEmotionEnv)
	if emotion == "" {
		emotion = defaultAvatarEmotion
	}
	return &Avatar{
		apiKey:         apiKey,
		avatar:         avatar,
		emotion:        emotion,
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAvatarTalkState,
	}
}

func (a *Avatar) Provider() string {
	return providerName
}

func (a *Avatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("AvatarTalk API key is required, either as argument or set AVATARTALK_API_KEY environment variable")
	}
	a.started = true
	return nil
}

func (a *Avatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

// Deprecated: use Avatar.
type AvatartalkAvatar = Avatar

// Deprecated: use NewAvatar.
func NewAvatartalkAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
