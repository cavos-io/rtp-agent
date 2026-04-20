package avatario

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type AvatarioAvatar struct {
	apiKey string
}

func NewAvatarioAvatar(apiKey string) *AvatarioAvatar {
	return &AvatarioAvatar{
		apiKey: apiKey,
	}
}

func (a *AvatarioAvatar) Start(ctx context.Context) error {
	fmt.Println("AvatarioAvatar started.")
	return nil
}


