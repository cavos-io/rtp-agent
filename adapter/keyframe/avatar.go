package keyframe

import (
	"context"
	"os"

	"github.com/cavos-io/rtp-agent/core/agent"
)

const (
	providerName               = "keyframe"
	defaultAPIURL              = "https://api.keyframelabs.com"
	defaultAvatarAgentIdentity = "keyframe-avatar-agent"
	defaultInitialAvatarState  = agent.AvatarStateIdle
	keyframeAPIKeyEnv          = "KEYFRAME_API_KEY"
	keyframeAPIURLEnv          = "KEYFRAME_API_URL"
)

type Avatar struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewAvatar(apiKey string) *Avatar {
	if apiKey == "" {
		apiKey = os.Getenv(keyframeAPIKeyEnv)
	}
	apiURL := os.Getenv(keyframeAPIURLEnv)
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	return &Avatar{
		apiKey:         apiKey,
		apiURL:         apiURL,
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
type KeyframeAgent = Avatar

// Deprecated: use NewAvatar.
func NewKeyframeAgent(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
