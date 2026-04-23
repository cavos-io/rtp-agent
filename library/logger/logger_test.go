package logger

import (
	"testing"
	"github.com/livekit/protocol/logger"
)

func TestLogger(t *testing.T) {
	if Logger == nil {
		t.Errorf("Logger should be initialized")
	}

	mock := logger.GetLogger()
	SetLogger(mock)
	if Logger != mock {
		t.Errorf("SetLogger failed")
	}
}
