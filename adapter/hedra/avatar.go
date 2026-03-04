package hedra

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/agent"
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
	fmt.Println("HedraAvatar started.")
	return nil
}

func (a *HedraAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("HedraAvatar state updated to: %s\n", state)
	return nil
}
