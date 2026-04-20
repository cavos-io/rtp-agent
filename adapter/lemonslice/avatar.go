package lemonslice

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type LemonsliceAvatar struct {
	apiKey string
}

func NewLemonsliceAvatar(apiKey string) *LemonsliceAvatar {
	return &LemonsliceAvatar{
		apiKey: apiKey,
	}
}

func (a *LemonsliceAvatar) Start(ctx context.Context) error {
	fmt.Println("LemonsliceAvatar started.")
	return nil
}


