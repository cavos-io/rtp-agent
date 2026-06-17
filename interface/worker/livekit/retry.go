package livekit

import (
	"fmt"
	"time"
)

func RetryDelay(retryCount int) time.Duration {
	delaySeconds := retryCount * 2
	if delaySeconds > 10 {
		delaySeconds = 10
	}
	return time.Duration(delaySeconds) * time.Second
}

func ConnectFailureError(agentURL string, attempts int, err error) error {
	return fmt.Errorf("failed to connect to LiveKit after %d attempts %s: %w", attempts, agentURL, err)
}
