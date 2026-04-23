package bey

import (
	"context"
	"testing"
)

func TestBeyAvatar_Start(t *testing.T) {
	a := NewBeyAvatar("fake-key")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
