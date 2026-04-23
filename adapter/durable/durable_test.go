package durable

import (
	"testing"
)

func TestInit(t *testing.T) {
	err := Init()
	if err != ErrNotSupported {
		t.Errorf("Expected ErrNotSupported, got %v", err)
	}
}
