package lemonslice

import (
	"context"
	"testing"
)

func TestLemonsliceAvatar_Start(t *testing.T) {
	a := NewLemonsliceAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}

func TestLemonsliceLLM_Initialization(t *testing.T) {
	l := NewLemonSliceLLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewLemonsliceLLM returned nil")
	}
}
