package llm

import (
	"sync"
	"time"
)

type LLMErrorHandler func(*LLMError)

type ErrorEmitter struct {
	mu       sync.Mutex
	nextID   uint64
	handlers []llmErrorHandlerSubscription
}

type llmErrorHandlerSubscription struct {
	id      uint64
	handler LLMErrorHandler
}

type errorCollectorLLM interface {
	OnError(LLMErrorHandler) func()
}

func (e *ErrorEmitter) OnError(handler LLMErrorHandler) func() {
	if handler == nil {
		return func() {}
	}
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.handlers = append(e.handlers, llmErrorHandlerSubscription{
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

func (e *ErrorEmitter) EmitError(err *LLMError) {
	if err == nil {
		return
	}
	if err.Type == "" {
		err.Type = "llm_error"
	}
	if err.Timestamp.IsZero() {
		err.Timestamp = time.Now()
	}

	e.mu.Lock()
	handlers := append([]llmErrorHandlerSubscription(nil), e.handlers...)
	e.mu.Unlock()

	for _, subscription := range handlers {
		subscription.handler(err)
	}
}
