package logger

import (
	"github.com/livekit/protocol/logger"
)

var Logger logger.Logger

func init() {
	logger.InitFromConfig(&logger.Config{Level: "debug"}, "worker")
	Logger = logger.GetLogger()
}

func SetLogger(l logger.Logger) {
	Logger = l
}
