package aws

import (
	"context"
	"testing"
)

func TestAWSLLM_Initialization(t *testing.T) {
	l, err := NewAWSLLM(context.Background(), "us-east-1", "")
	if err != nil {
		t.Fatalf("NewAWSLLM failed: %v", err)
	}
	if l.model != "anthropic.claude-3-haiku-20240307-v1:0" {
		t.Errorf("Expected default model, got %s", l.model)
	}
}
