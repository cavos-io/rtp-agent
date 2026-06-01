package trugen

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type TrugenAvatar struct {
	apiKey string
}

func NewTrugenAvatar(apiKey string) *TrugenAvatar {
	return &TrugenAvatar{
		apiKey: apiKey,
	}
}

func (a *TrugenAvatar) Start(ctx context.Context) error {
	fmt.Println("TrugenAvatar started.")
	return nil
}

func (a *TrugenAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("TrugenAvatar state updated to: %s\n", state)
	return nil
}
