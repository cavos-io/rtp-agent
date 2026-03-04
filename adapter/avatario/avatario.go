package avatario

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/agent"
)

type AvatarioAvatar struct {
	apiKey string
}

func NewAvatarioAvatar(apiKey string) *AvatarioAvatar {
	return &AvatarioAvatar{
		apiKey: apiKey,
	}
}

func (a *AvatarioAvatar) Start(ctx context.Context) error {
	fmt.Println("AvatarioAvatar started.")
	return nil
}

func (a *AvatarioAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("AvatarioAvatar state updated to: %s\n", state)
	return nil
}
