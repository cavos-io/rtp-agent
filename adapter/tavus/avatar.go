package tavus

import (
	"context"
	"fmt"
<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type TavusAvatar struct {
	apiKey string
}

func NewTavusAvatar(apiKey string) *TavusAvatar {
	return &TavusAvatar{
		apiKey: apiKey,
	}
}

func (a *TavusAvatar) Start(ctx context.Context) error {
	fmt.Println("TavusAvatar started.")
	return nil
}


