package bey

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type BeyAvatar struct {
	apiKey string
}

func NewBeyAvatar(apiKey string) *BeyAvatar {
	return &BeyAvatar{
		apiKey: apiKey,
	}
}

func (a *BeyAvatar) Start(ctx context.Context) error {
	fmt.Println("BeyAvatar started.")
	return nil
}


