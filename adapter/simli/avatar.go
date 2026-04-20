package simli

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type SimliAvatar struct {
	apiKey string
}

func NewSimliAvatar(apiKey string) *SimliAvatar {
	return &SimliAvatar{
		apiKey: apiKey,
	}
}

func (a *SimliAvatar) Start(ctx context.Context) error {
	fmt.Println("SimliAvatar started.")
	return nil
}


