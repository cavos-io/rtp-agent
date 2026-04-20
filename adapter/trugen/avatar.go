package trugen

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type TrugenAvatar struct {
	apiKey string
}

func NewTrugenAvatar(apiKey string) *TrugenAvatar {
	return &TrugenAvatar{
		apiKey: apiKey,
	}
}

func (a *TrugenAvatar) Start(ctx context.Context) error {
	fmt.Println("TrugenAvatar started.")
	return nil
}


