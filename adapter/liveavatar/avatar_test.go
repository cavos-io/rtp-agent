package liveavatar

import (
	"context"
	"testing"
)

func TestLiveAvatar_Start(t *testing.T) {
	a := NewLiveAvatar("fake-key")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
