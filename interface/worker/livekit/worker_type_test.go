package livekit_test

import (
	"testing"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	lkprotocol "github.com/livekit/protocol/livekit"
)

func TestJobTypeForWorkerType(t *testing.T) {
	tests := []struct {
		name       string
		workerType string
		want       lkprotocol.JobType
	}{
		{name: "default", workerType: "", want: lkprotocol.JobType_JT_ROOM},
		{name: "room", workerType: "room", want: lkprotocol.JobType_JT_ROOM},
		{name: "publisher", workerType: "publisher", want: lkprotocol.JobType_JT_PUBLISHER},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerlivekit.JobTypeForWorkerType(tt.workerType); got != tt.want {
				t.Fatalf("JobTypeForWorkerType(%q) = %v, want %v", tt.workerType, got, tt.want)
			}
		})
	}
}

func TestJobTypeNameForWorkerType(t *testing.T) {
	tests := []struct {
		name       string
		workerType string
		want       string
	}{
		{name: "default", workerType: "", want: "JT_ROOM"},
		{name: "room", workerType: "room", want: "JT_ROOM"},
		{name: "publisher", workerType: "publisher", want: "JT_PUBLISHER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workerlivekit.JobTypeNameForWorkerType(tt.workerType); got != tt.want {
				t.Fatalf("JobTypeNameForWorkerType(%q) = %q, want %q", tt.workerType, got, tt.want)
			}
		})
	}
}
