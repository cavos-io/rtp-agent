package livekit_test

import (
	"errors"
	"strings"
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

func TestConnectFailureErrorIncludesReferenceMessageAndCause(t *testing.T) {
	cause := errors.New("dial failed")
	err := workerlivekit.ConnectFailureError("wss://livekit.example/agent", 3, cause)
	if err == nil {
		t.Fatal("ConnectFailureError() = nil, want error")
	}
	if !strings.Contains(err.Error(), "failed to connect to LiveKit after 3 attempts wss://livekit.example/agent") {
		t.Fatalf("ConnectFailureError() = %q, want LiveKit failure message", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatalf("ConnectFailureError() does not wrap cause")
	}
}
