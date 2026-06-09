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

type KeyframeAgent struct {
	apiKey         string
	apiURL         string
	avatarIdentity string
	state          agent.AvatarState
}

func NewKeyframeAgent(apiKey string) *KeyframeAgent {
	if apiKey == "" {
		apiKey = os.Getenv(keyframeAPIKeyEnv)
	}
	apiURL := os.Getenv(keyframeAPIURLEnv)
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	return &KeyframeAgent{
		apiKey:         apiKey,
		apiURL:         apiURL,
		avatarIdentity: defaultAvatarAgentIdentity,
		state:          defaultInitialAvatarState,
	}
}

func (a *KeyframeAgent) Start(ctx context.Context) error {
	return nil
}

func (a *KeyframeAgent) UpdateState(state agent.AvatarState) error {
	a.state = state
	return nil
}

func (a *KeyframeAgent) Provider() string {
	return providerName
}

func (a *KeyframeAgent) AvatarIdentity() string {
	return a.avatarIdentity
}
