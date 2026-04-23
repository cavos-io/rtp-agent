package turndetector

import (
	"context"
	"testing"
)

func TestEOUPredictor(t *testing.T) {
	p := NewEOUPredictor("")
	
	_, err := p.Detect(context.Background(), "hello world")
	if err == nil {
		t.Errorf("Expected error from Detect, got nil")
	}
}
