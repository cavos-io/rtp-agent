package hedra

import (
	"testing"
)

func TestNewHedraLLM(t *testing.T) {
	l := NewHedraLLM("key", "model")
	if l == nil {
		t.Fatal("Expected HedraLLM instance, got nil")
	}
}
