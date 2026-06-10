package stt

import (
	"fmt"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func callSTTMetricsHandler(handler STTMetricsHandler, metrics *telemetry.STTMetrics) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit STT metrics event", sttPanicAsError(recovered))
		}
	}()
	handler(metrics)
}

func callSTTErrorHandler(handler STTErrorHandler, err *STTError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit STT error event", sttPanicAsError(recovered))
		}
	}()
	handler(err)
}

func callAvailabilityChangedHandler(handler AvailabilityChangedHandler, event AvailabilityChangedEvent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit STT fallback availability event", sttPanicAsError(recovered))
		}
	}()
	handler(event)
}

func sttPanicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}
