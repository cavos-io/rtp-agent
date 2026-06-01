package tts

import (
	"sync"
	"time"
)

const TTSErrorType = "tts_error"

type TTSError struct {
	Type        string
	Timestamp   time.Time
	Label       string
	Err         error
	Recoverable bool
}

type TTSErrorHandler func(TTSError)

type ErrorEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []ttsErrorHandlerSubscription
}

type ttsErrorHandlerSubscription struct {
	id      uint64
	handler TTSErrorHandler
}

type errorCollectorTTS interface {
	OnError(TTSErrorHandler) func()
}

type errorEmitterTTS interface {
	EmitError(TTSError)
}

func emitTTSError(provider TTS, err error, recoverable bool) {
	if provider == nil || err == nil {
		return
	}
	emitter, ok := provider.(errorEmitterTTS)
	if !ok {
		return
	}
	emitter.EmitError(TTSError{
		Label:       provider.Label(),
		Err:         err,
		Recoverable: recoverable,
	})
}

func (e *ErrorEmitter) OnError(handler TTSErrorHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, ttsErrorHandlerSubscription{
		id:      id,
		handler: handler,
	})
	e.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			e.removeErrorHandler(id)
		})
	}
}

func (e *ErrorEmitter) removeErrorHandler(id uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, subscription := range e.handlers {
		if subscription.id == id {
			e.handlers = append(e.handlers[:i], e.handlers[i+1:]...)
			return
		}
	}
}

func (e *ErrorEmitter) EmitError(err TTSError) {
	if err.Type == "" {
		err.Type = TTSErrorType
	}
	if err.Timestamp.IsZero() {
		err.Timestamp = time.Now()
	}

	e.mu.Lock()
	handlers := append([]ttsErrorHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		subscription.handler(err)
	}
}
