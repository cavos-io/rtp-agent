package livekit_test

import (
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
)

func TestRetryDelayMatchesReferenceBackoff(t *testing.T) {
	tests := []struct {
		retryCount int
		want       time.Duration
	}{
		{retryCount: 0, want: 0},
		{retryCount: 1, want: 2 * time.Second},
		{retryCount: 5, want: 10 * time.Second},
		{retryCount: 8, want: 10 * time.Second},
	}

	for _, test := range tests {
		if got := workerlivekit.RetryDelay(test.retryCount); got != test.want {
			t.Fatalf("RetryDelay(%d) = %v, want %v", test.retryCount, got, test.want)
		}
	}
}
