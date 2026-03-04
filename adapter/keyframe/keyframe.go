package keyframe

import (
	"context"
	"fmt"
	"github.com/cavos-io/conversation-worker/core/agent"
)

type KeyframeAgent struct {
	apiKey string
}

func NewKeyframeAgent(apiKey string) *KeyframeAgent {
	return &KeyframeAgent{
		apiKey: apiKey,
	}
}

func (a *KeyframeAgent) Start(ctx context.Context) error {
	fmt.Println("KeyframeAgent started.")
	return nil
}

func (a *KeyframeAgent) UpdateState(state agent.AvatarState) error {
	fmt.Printf("KeyframeAgent state updated to: %s\n", state)
	return nil
}
