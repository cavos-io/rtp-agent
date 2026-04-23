package aws

import (
	"context"
	"testing"
)

func TestAWSSTT_Initialization(t *testing.T) {
	s, err := NewAWSSTT(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("NewAWSSTT failed: %v", err)
	}
	if s.Label() != "aws.STT" {
		t.Errorf("Expected aws.STT, got %s", s.Label())
	}
}
