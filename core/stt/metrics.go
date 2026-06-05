package stt

import (
	"sync"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type STTMetricsHandler func(*telemetry.STTMetrics)

type MetricsEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []sttMetricsHandlerSubscription
}

type sttMetricsHandlerSubscription struct {
	id      uint64
	handler STTMetricsHandler
}

type metricsCollectorSTT interface {
	OnMetricsCollected(STTMetricsHandler) func()
}

func (e *MetricsEmitter) OnMetricsCollected(handler STTMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, sttMetricsHandlerSubscription{
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

func (e *MetricsEmitter) EmitMetricsCollected(metrics *telemetry.STTMetrics) {
	e.mu.Lock()
	handlers := append([]sttMetricsHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		subscription.handler(metrics)
	}
}
