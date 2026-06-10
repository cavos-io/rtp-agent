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

type AvatartalkAvatar struct {
	apiKey         string
	avatar         string
	emotion        string
	avatarIdentity string
	avatarName     string
	state          agent.AvatarState
	started        bool
}

func NewAvatartalkAvatar(apiKey string) *AvatartalkAvatar {
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
	return &AvatartalkAvatar{
		apiKey:         apiKey,
		avatar:         avatar,
		emotion:        emotion,
		avatarIdentity: defaultAvatarAgentIdentity,
		avatarName:     defaultAvatarAgentName,
		state:          defaultInitialAvatarTalkState,
	}
}

func (a *AvatartalkAvatar) Provider() string {
	return providerName
}

func (a *AvatartalkAvatar) Start(ctx context.Context) error {
	if a.apiKey == "" {
		return errors.New("AvatarTalk API key is required, either as argument or set AVATARTALK_API_KEY environment variable")
	}
	a.started = true
	return nil
}

func (a *AvatartalkAvatar) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}
