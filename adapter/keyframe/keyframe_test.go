package keyframe

import (
	"context"
	"testing"
)

func TestKeyframeAvatar_Start(t *testing.T) {
	a := NewKeyframeAgent("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}
