package simli

import (
	"testing"
)

func TestNewSimliLLM(t *testing.T) {
	l := NewSimliLLM("key", "model")
	if l == nil {
		t.Fatal("Expected SimliLLM instance, got nil")
	}
}
