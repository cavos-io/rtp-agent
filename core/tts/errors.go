package tts

import (
	"encoding/json"
	"fmt"
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

func (e TTSError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Label != "" {
		return e.Label
	}
	if e.Type != "" {
		return e.Type
	}
	return TTSErrorType
}

func (e TTSError) Unwrap() error {
	return e.Err
}

func (e TTSError) MarshalJSON() ([]byte, error) {
	type ttsErrorPayload struct {
		Type        string  `json:"type"`
		Timestamp   float64 `json:"timestamp"`
		Label       string  `json:"label"`
		Recoverable bool    `json:"recoverable"`
	}
	if e.Type == "" {
		e.Type = TTSErrorType
	}
	return json.Marshal(ttsErrorPayload{
		Type:        e.Type,
		Timestamp:   float64(e.Timestamp.UnixNano()) / float64(time.Second),
		Label:       e.Label,
		Recoverable: e.Recoverable,
	})
}

func (e *TTSError) UnmarshalJSON(data []byte) error {
	type ttsErrorPayload struct {
		Type        string   `json:"type"`
		Timestamp   *float64 `json:"timestamp"`
		Label       *string  `json:"label"`
		Recoverable *bool    `json:"recoverable"`
	}
	var payload ttsErrorPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Timestamp == nil {
		return fmt.Errorf("tts error timestamp is required")
	}
	if payload.Label == nil {
		return fmt.Errorf("tts error label is required")
	}
	if payload.Recoverable == nil {
		return fmt.Errorf("tts error recoverable is required")
	}

	e.Type = payload.Type
	if e.Type == "" {
		e.Type = TTSErrorType
	}
	e.Timestamp = time.Unix(0, int64(*payload.Timestamp*float64(time.Second)))
	e.Label = *payload.Label
	e.Recoverable = *payload.Recoverable
	e.Err = nil
	return nil
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
		callTTSErrorHandler(subscription.handler, err)
	}
}
