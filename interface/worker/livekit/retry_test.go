package livekit_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	workerlivekit "github.com/cavos-io/rtp-agent/interface/worker/livekit"
	"github.com/gorilla/websocket"
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
	if !strings.Contains(err.Error(), "failed to connect to livekit after 3 attempts wss://livekit.example/agent") {
		t.Fatalf("ConnectFailureError() = %q, want reference livekit failure message", err.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatalf("ConnectFailureError() does not wrap cause")
	}
	if !workerlivekit.IsConnectFailure(err) {
		t.Fatalf("ConnectFailureError() is not classified as connect failure")
	}
}

func TestConnectWorkerWebSocketRetriesDialFailures(t *testing.T) {
	attempts := 0
	dial := func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
		attempts++
		if attempts < 3 {
			return nil, nil, errors.New("dial failed")
		}
		return &websocket.Conn{}, nil, nil
	}

	var sleeps []time.Duration
	sleep := func(_ context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return nil
	}

	conn, _, err := workerlivekit.ConnectWorkerWebSocket(context.Background(), workerlivekit.WorkerWebSocketConnectOptions{
		Dialer:   &websocket.Dialer{},
		URL:      "wss://livekit.example/agent",
		MaxRetry: 3,
		Dial:     dial,
		Sleep:    sleep,
	})
	if err != nil {
		t.Fatalf("ConnectWorkerWebSocket() error = %v", err)
	}
	if conn == nil {
		t.Fatal("ConnectWorkerWebSocket() conn = nil")
	}
	if attempts != 3 {
		t.Fatalf("dial attempts = %d, want 3", attempts)
	}
	wantSleeps := []time.Duration{0, 2 * time.Second}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("retry sleeps = %v, want %v", sleeps, wantSleeps)
	}
	for i := range wantSleeps {
		if sleeps[i] != wantSleeps[i] {
			t.Fatalf("retry sleeps = %v, want %v", sleeps, wantSleeps)
		}
	}
}

func TestConnectWorkerWebSocketReturnsSleepErrorWithoutConnectFailure(t *testing.T) {
	sleepErr := context.Canceled
	_, _, err := workerlivekit.ConnectWorkerWebSocket(context.Background(), workerlivekit.WorkerWebSocketConnectOptions{
		Dialer:   &websocket.Dialer{},
		URL:      "wss://livekit.example/agent",
		MaxRetry: 1,
		Dial: func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error) {
			return nil, nil, errors.New("dial failed")
		},
		Sleep: func(context.Context, time.Duration) error {
			return sleepErr
		},
	})
	if !errors.Is(err, sleepErr) {
		t.Fatalf("ConnectWorkerWebSocket() error = %v, want sleep error", err)
	}
	if workerlivekit.IsConnectFailure(err) {
		t.Fatalf("ConnectWorkerWebSocket() marked sleep error as connect failure")
	}
}
