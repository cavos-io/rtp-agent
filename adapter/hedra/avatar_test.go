package hedra

import (
	"context"
	"testing"
)

func TestHedraAvatar_Start(t *testing.T) {
	a := NewHedraAvatar("fake-key")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
