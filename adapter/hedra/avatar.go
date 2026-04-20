package hedra

import (
	"context"
	"fmt"

<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/core/agent"
>>>>>>> origin/main
)

type HedraAvatar struct {
	apiKey string
}

func NewHedraAvatar(apiKey string) *HedraAvatar {
	return &HedraAvatar{
		apiKey: apiKey,
	}
}

func (a *HedraAvatar) Start(ctx context.Context) error {
	fmt.Println("HedraAvatar started.")
	return nil
}


