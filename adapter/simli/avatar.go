package simli

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type SimliAvatar struct {
	apiKey string
}

func NewSimliAvatar(apiKey string) *SimliAvatar {
	return &SimliAvatar{
		apiKey: apiKey,
	}
}

func (a *SimliAvatar) Start(ctx context.Context) error {
	fmt.Println("SimliAvatar started.")
	return nil
}

func (a *SimliAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("SimliAvatar state updated to: %s\n", state)
	return nil
}
