package tts

import (
	"sync"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type TTSMetricsHandler func(*telemetry.TTSMetrics)

type MetricsEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []ttsMetricsHandlerSubscription
}

type ttsMetricsHandlerSubscription struct {
	id      uint64
	handler TTSMetricsHandler
}

type metricsCollectorTTS interface {
	OnMetricsCollected(TTSMetricsHandler) func()
}

type metricsEmitterTTS interface {
	EmitMetricsCollected(*telemetry.TTSMetrics)
}

func (e *MetricsEmitter) OnMetricsCollected(handler TTSMetricsHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, ttsMetricsHandlerSubscription{
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

func (e *MetricsEmitter) EmitMetricsCollected(metrics *telemetry.TTSMetrics) {
	e.mu.Lock()
	handlers := append([]ttsMetricsHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		subscription.handler(metrics)
	}
}
