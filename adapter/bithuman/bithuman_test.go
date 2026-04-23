package bithuman

import (
	"context"
	"testing"
)

func TestBithumanAvatar_Start(t *testing.T) {
	a := NewBithumanAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
