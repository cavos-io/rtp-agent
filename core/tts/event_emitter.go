package tts

import (
	"fmt"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func callTTSMetricsHandler(handler TTSMetricsHandler, metrics *telemetry.TTSMetrics) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit TTS metrics event", ttsPanicAsError(recovered))
		}
	}()
	handler(metrics)
}

func callTTSErrorHandler(handler TTSErrorHandler, err TTSError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit TTS error event", ttsPanicAsError(recovered))
		}
	}()
	handler(err)
}

func callAvailabilityChangedHandler(handler AvailabilityChangedHandler, event AvailabilityChangedEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit TTS fallback availability event", ttsPanicAsError(recovered))
		}
	}()
	handler(event)
}

func ttsPanicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}
