package stt

import (
	"sync"
	"time"
)

type STTErrorHandler func(*STTError)

type ErrorEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []sttErrorHandlerSubscription
}

type sttErrorHandlerSubscription struct {
	id      uint64
	handler STTErrorHandler
}

type errorCollectorSTT interface {
	OnError(STTErrorHandler) func()
}

func (e *ErrorEmitter) OnError(handler STTErrorHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, sttErrorHandlerSubscription{
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

func (e *ErrorEmitter) EmitError(err *STTError) {
	if err == nil {
		return
	}
	if err.Type == "" {
		err.Type = STTErrorType
	}
	if err.Timestamp.IsZero() {
		err.Timestamp = time.Now()
	}

	e.mu.Lock()
	handlers := append([]sttErrorHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		callSTTErrorHandler(subscription.handler, err)
	}
}
