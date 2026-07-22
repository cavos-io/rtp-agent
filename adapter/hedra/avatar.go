package hedra

import (
	"context"
	"errors"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type Avatar struct {
	apiKey string
}

func NewAvatar(apiKey string) *Avatar {
	return &Avatar{
		apiKey: apiKey,
	}
}

func (a *Avatar) Start(ctx context.Context) error {
	return errors.New("hedra realtime avatar service has been disabled: this plugin no longer functions; browse other avatar integrations at https://docs.livekit.io/agents/models/avatar/")
}

func (a *Avatar) UpdateState(state agent.AvatarState) error {
	return nil
}

// Deprecated: use Avatar.
type HedraAvatar = Avatar

// Deprecated: use NewAvatar.
func NewHedraAvatar(apiKey string) *Avatar {
	return NewAvatar(apiKey)
}
