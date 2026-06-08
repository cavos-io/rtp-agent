package hedra

import (
	"context"
	"errors"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type HedraAvatar struct {
	apiKey string
}

func NewHedraAvatar(apiKey string) *HedraAvatar {
	return &HedraAvatar{
		apiKey: apiKey,
	}
}

func (a *HedraAvatar) Start(ctx context.Context) error {
	return errors.New("hedra realtime avatar service has been disabled: this plugin no longer functions; browse other avatar integrations at https://docs.livekit.io/agents/models/avatar/")
}

func (a *HedraAvatar) UpdateState(state agent.AvatarState) error {
	return nil
}
