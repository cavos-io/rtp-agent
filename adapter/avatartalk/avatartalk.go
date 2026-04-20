package avatartalk

import (
	"context"
	"fmt"

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


