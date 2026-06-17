package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestAgentIdentityForJobID(t *testing.T) {
	tests := []struct {
		jobID string
		want  string
	}{
		{jobID: "job_123456789", want: "agent-job_123456789"},
		{jobID: "abc", want: "agent-abc"},
	}

	for _, tt := range tests {
		if got := workerlivekit.AgentIdentityForJobID(tt.jobID); got != tt.want {
			t.Fatalf("AgentIdentityForJobID(%q) = %q, want %q", tt.jobID, got, tt.want)
		}
	}
}
