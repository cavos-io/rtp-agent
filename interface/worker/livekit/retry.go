package livekit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

func RetryDelay(retryCount int) time.Duration {
	delaySeconds := retryCount * 2
	if delaySeconds > 10 {
		delaySeconds = 10
	}
	return time.Duration(delaySeconds) * time.Second
}

func ConnectFailureError(agentURL string, attempts int, err error) error {
	return &connectFailureError{
		agentURL: agentURL,
		attempts: attempts,
		cause:    err,
	}
}

type connectFailureError struct {
	agentURL string
	attempts int
	cause    error
}

func (e *connectFailureError) Error() string {
	return fmt.Sprintf("failed to connect to livekit after %d attempts %s: %v", e.attempts, e.agentURL, e.cause)
}

func (e *connectFailureError) Unwrap() error {
	return e.cause
}

func IsConnectFailure(err error) bool {
	var failure *connectFailureError
	return errors.As(err, &failure)
}

type WorkerWebSocketDialFunc func(context.Context, *websocket.Dialer, string, http.Header) (*websocket.Conn, *http.Response, error)

type WorkerWebSocketSleepFunc func(context.Context, time.Duration) error

type WorkerWebSocketConnectOptions struct {
	Dialer   *websocket.Dialer
	URL      string
	Headers  http.Header
	MaxRetry int
	Dial     WorkerWebSocketDialFunc
	Sleep    WorkerWebSocketSleepFunc
}

type WorkerWebSocketOpenOptions struct {
	WSURL       string
	WorkerToken string
	APIKey      string
	APISecret   string
	TTL         time.Duration
	HTTPProxy   string
	MaxRetry    int
	Dial        WorkerWebSocketDialFunc
	Sleep       WorkerWebSocketSleepFunc
}

type WorkerWebSocketOpenResult struct {
	Conn          *websocket.Conn
	Response      *http.Response
	ConnectFailed bool
}

func OpenWorkerWebSocket(ctx context.Context, opts WorkerWebSocketOpenOptions) (WorkerWebSocketOpenResult, error) {
	connectInfo, err := WorkerConnectInfo(WorkerConnectOptions{
		WSURL:       opts.WSURL,
		WorkerToken: opts.WorkerToken,
		APIKey:      opts.APIKey,
		APISecret:   opts.APISecret,
		TTL:         opts.TTL,
	})
	if err != nil {
		return WorkerWebSocketOpenResult{}, err
	}

	dialer, err := WorkerWebSocketDialer(opts.HTTPProxy)
	if err != nil {
		return WorkerWebSocketOpenResult{}, err
	}

	conn, res, err := ConnectWorkerWebSocket(ctx, WorkerWebSocketConnectOptions{
		Dialer:   dialer,
		URL:      connectInfo.URL,
		Headers:  connectInfo.Header,
		MaxRetry: opts.MaxRetry,
		Dial:     opts.Dial,
		Sleep:    opts.Sleep,
	})
	if err != nil {
		return WorkerWebSocketOpenResult{ConnectFailed: IsConnectFailure(err)}, err
	}
	return WorkerWebSocketOpenResult{Conn: conn, Response: res}, nil
}

func ConnectWorkerWebSocket(ctx context.Context, opts WorkerWebSocketConnectOptions) (*websocket.Conn, *http.Response, error) {
	dial := opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, dialer *websocket.Dialer, url string, headers http.Header) (*websocket.Conn, *http.Response, error) {
			return dialer.DialContext(ctx, url, headers)
		}
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}

	retryCount := 0
	for {
		conn, res, err := dial(ctx, opts.Dialer, opts.URL, opts.Headers)
		if err == nil {
			return conn, res, nil
		}
		if retryCount >= opts.MaxRetry {
			return nil, nil, ConnectFailureError(opts.URL, retryCount, err)
		}
		delay := RetryDelay(retryCount)
		retryCount++
		if err := sleep(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}
