package lemonslice

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type LemonsliceAvatar struct {
	apiKey string
}

func NewLemonsliceAvatar(apiKey string) *LemonsliceAvatar {
	return &LemonsliceAvatar{
		apiKey: apiKey,
	}
}

func (a *LemonsliceAvatar) Start(ctx context.Context) error {
	fmt.Println("LemonsliceAvatar started.")
	return nil
}

func (a *LemonsliceAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("LemonsliceAvatar state updated to: %s\n", state)
	return nil
}
