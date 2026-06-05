package llm

import (
	"sync"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type LLMMetricsHandler func(*telemetry.LLMMetrics)

type MetricsEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []llmMetricsHandlerSubscription
}

type llmMetricsHandlerSubscription struct {
	id      uint64
	handler LLMMetricsHandler
}

type metricsCollectorLLM interface {
	OnMetricsCollected(LLMMetricsHandler) func()
}

func (e *MetricsEmitter) OnMetricsCollected(handler LLMMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, llmMetricsHandlerSubscription{
		id:      id,
		handler: handler,
	})
	e.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.removeMetricsHandler(id)
		})
	}
}

func (e *MetricsEmitter) removeMetricsHandler(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, subscription := range e.handlers {
		if subscription.id == id {
			e.handlers = append(e.handlers[:i], e.handlers[i+1:]...)
			return
		}
	}
}

func (e *MetricsEmitter) EmitMetricsCollected(metrics *telemetry.LLMMetrics) {
	e.mu.Lock()
	handlers := append([]llmMetricsHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		subscription.handler(metrics)
	}
}
