package avatario

import (
	"context"
	"testing"
)

func TestAvatarioAvatar_Start(t *testing.T) {
	a := NewAvatarioAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
