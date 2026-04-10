package avatartalk

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type AvatartalkAvatar struct {
	apiKey string
}

func NewAvatartalkAvatar(apiKey string) *AvatartalkAvatar {
	return &AvatartalkAvatar{
		apiKey: apiKey,
	}
}

func (a *AvatartalkAvatar) Start(ctx context.Context) error {
	fmt.Println("AvatartalkAvatar started.")
	return nil
}

func (a *AvatartalkAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("AvatartalkAvatar state updated to: %s\n", state)
	return nil
}
