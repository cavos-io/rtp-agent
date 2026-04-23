package anam

import (
	"context"
	"testing"
)

func TestAnamAvatar_Start(t *testing.T) {
	a := NewAnamAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
