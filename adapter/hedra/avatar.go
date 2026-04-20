package hedra

import (
	"context"
	"fmt"

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


