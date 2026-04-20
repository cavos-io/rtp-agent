package keyframe

import (
	"context"
	"fmt"
<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
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
	fmt.Println("KeyframeAvatar started.")
	return nil
	}

