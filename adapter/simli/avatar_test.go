package simli

import (
	"context"
	"testing"
)

func TestSimliAvatar_Start(t *testing.T) {
	a := NewSimliAvatar("fake-key")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
