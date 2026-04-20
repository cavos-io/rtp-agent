package anam

import (
	"context"
	"fmt"
<<<<<<< HEAD
=======

	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
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
