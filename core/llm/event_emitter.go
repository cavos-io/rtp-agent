package llm

import (
	"fmt"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func callLLMMetricsHandler(handler LLMMetricsHandler, metrics *telemetry.LLMMetrics) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit LLM metrics event", llmPanicAsError(recovered))
		}
	}()
	handler(metrics)
}

func callLLMErrorHandler(handler LLMErrorHandler, err *LLMError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Logger.Warnw("failed to emit LLM error event", llmPanicAsError(recovered))
		}
	}()
	handler(err)
}

func llmPanicAsError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return fmt.Errorf("%v", recovered)
}
