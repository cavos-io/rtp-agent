package bithuman

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/agent"
)

type BithumanAvatar struct {
	apiKey string
}

func NewBithumanAvatar(apiKey string) *BithumanAvatar {
	return &BithumanAvatar{
		apiKey: apiKey,
	}
}

func (a *BithumanAvatar) Start(ctx context.Context) error {
	fmt.Println("BithumanAvatar started.")
	return nil
}

func (a *BithumanAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("BithumanAvatar state updated to: %s\n", state)
	return nil
}
