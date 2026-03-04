package anam

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/agent"
)

type AnamAvatar struct {
	apiKey string
}

func NewAnamAvatar(apiKey string) *AnamAvatar {
	return &AnamAvatar{
		apiKey: apiKey,
	}
}

func (a *AnamAvatar) Start(ctx context.Context) error {
	fmt.Println("AnamAvatar started.")
	return nil
}

func (a *AnamAvatar) UpdateState(state agent.AvatarState) error {
	fmt.Printf("AnamAvatar state updated to: %s\n", state)
	return nil
}
