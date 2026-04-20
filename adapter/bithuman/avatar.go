package bithuman

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type BithumanAvatar struct {
	apiKey string
}

func NewBithumanAvatar(apiKey string) *BithumanAvatar {
	return &BithumanAvatar{
		apiKey: apiKey,
	}
}

func (a *BithumanAvatar) Start(ctx context.Context) error {
	fmt.Println("BithumanAvatar started.")
	return nil
}


