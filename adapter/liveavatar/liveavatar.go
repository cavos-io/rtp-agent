package liveavatar

import (
	"context"
	"fmt"
	"github.com/cavos-io/conversation-worker/core/agent"
)

type LiveAvatar struct {
	apiKey string
}

func NewLiveAvatar(apiKey string) *LiveAvatar {
	return &LiveAvatar{
		apiKey: apiKey,
	}
}

func (a *LiveAvatar) Start(ctx context.Context) error {
	fmt.Println("LiveAvatar started.")
	return nil
}

func (a *LiveAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("LiveAvatar state updated to: %s\n", state)
	return nil
}
