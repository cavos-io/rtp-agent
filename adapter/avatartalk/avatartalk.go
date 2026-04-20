package avatartalk

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type AvatartalkAvatar struct {
	apiKey string
}

func NewAvatartalkAvatar(apiKey string) *AvatartalkAvatar {
	return &AvatartalkAvatar{
		apiKey: apiKey,
	}
}

func (a *AvatartalkAvatar) Start(ctx context.Context) error {
	fmt.Println("AvatartalkAvatar started.")
	return nil
}


