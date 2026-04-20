package lemonslice

import (
	"context"
	"fmt"
)

type LemonsliceAvatar struct {
	apiKey string
}

func NewLemonsliceAvatar(apiKey string) *LemonsliceAvatar {
	return &LemonsliceAvatar{
		apiKey: apiKey,
	}
}

func (a *LemonsliceAvatar) Start(ctx context.Context) error {
	fmt.Println("LemonsliceAvatar started.")
	return nil
}


