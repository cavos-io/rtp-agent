package agent

import (
	"testing"
)

func TestUploadSessionReport_SkipNonCloud(t *testing.T) {
	report := NewSessionReport()
	
	// Should skip and return nil for non-cloud URL
	err := UploadSessionReport("http://localhost:8080", "key", "secret", "agent", report)
	if err != nil {
		t.Errorf("Expected nil error for non-cloud URL, got %v", err)
	}
}
