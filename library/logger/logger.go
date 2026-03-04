package logger

import (
	"github.com/livekit/protocol/logger"
)

var Logger = logger.GetLogger()

func SetLogger(l logger.Logger) {
	Logger = l
}
