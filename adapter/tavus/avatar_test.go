package tavus

import (
	"context"
	"testing"
)

func TestTavusAvatar_Start(t *testing.T) {
	a := NewTavusAvatar("fake-key")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
