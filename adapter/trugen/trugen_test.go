package trugen

import (
	"context"
	"testing"
)

func TestTrugenAvatar_Start(t *testing.T) {
	a := NewTrugenAvatar("apiKey")
	err := a.Start(context.Background())
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}
}

func TestTrugenLLM_Initialization(t *testing.T) {
	l := NewTrugenLLM("apiKey", "model")
	if l == nil {
		t.Fatal("NewTrugenLLM returned nil")
	}
}
