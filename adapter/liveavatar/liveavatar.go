package liveavatar

import (
	"context"
	"fmt"
)

type LiveAvatar struct {
	apiKey string
}

func NewLiveAvatar(apiKey string) *LiveAvatar {
	return &LiveAvatar{
		apiKey: apiKey,
	}
}

func (a *LiveAvatar) Start(ctx context.Context) error {
	fmt.Println("LiveAvatar started.")
	return nil
}

