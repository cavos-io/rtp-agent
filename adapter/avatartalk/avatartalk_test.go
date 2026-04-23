package avatartalk

import (
	"context"
	"testing"
)

func TestAvatartalkAvatar_Start(t *testing.T) {
	a := NewAvatartalkAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
