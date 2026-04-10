package tavus

import (
	"context"
	"fmt"
	"github.com/cavos-io/rtp-agent/core/agent"
)

type TavusAvatar struct {
	apiKey string
}

func NewTavusAvatar(apiKey string) *TavusAvatar {
	return &TavusAvatar{
		apiKey: apiKey,
	}
}

func (a *TavusAvatar) Start(ctx context.Context) error {
	fmt.Println("TavusAvatar started.")
	return nil
}

func (a *TavusAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("TavusAvatar state updated to: %s\n", state)
	return nil
}
