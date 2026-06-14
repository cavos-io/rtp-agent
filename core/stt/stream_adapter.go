package stt

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// StreamAdapter converts a non-streaming STT into a streaming STT by coupling it with a VAD.
// It buffers audio frames and sends them to the underlying STT Recognize method when the VAD detects speech.
type StreamAdapter struct {
	MetricsEmitter
	ErrorEmitter
	mu                  sync.Mutex
	stt                 STT
	vad                 vad.VAD
	metricsUnsubscribes []func()
}

func NewStreamAdapter(stt STT, vad vad.VAD) *StreamAdapter {
	return &StreamAdapter{
		stt: stt,
		vad: vad,
	}
}

func (a *StreamAdapter) Label() string {
	return fmt.Sprintf("StreamAdapter(%s)", a.stt.Label())
}

func (a *StreamAdapter) Capabilities() STTCapabilities {
	return STTCapabilities{Streaming: true, OfflineRecognize: true}
}

func (a *StreamAdapter) WrappedSTT() STT {
	return a.stt
}

func (a *StreamAdapter) Model() string {
	return Model(a.stt)
}

func (a *StreamAdapter) Provider() string {
	return Provider(a.stt)
}

func (a *StreamAdapter) Prewarm() {
	Prewarm(a.stt)
}

func (a *StreamAdapter) Close() error {
	a.mu.Lock()
	unsubscribes := append([]func(){}, a.metricsUnsubscribes...)
	a.metricsUnsubscribes = nil
	a.mu.Unlock()

	for _, unsubscribe := range unsubscribes {
		unsubscribe()
	}
	return nil
}

func (a *StreamAdapter) OnMetricsCollected(handler STTMetricsHandler) func() {
	localUnsubscribe := a.MetricsEmitter.OnMetricsCollected(handler)
	providerUnsubscribes := make([]func(), 0, 1)
	if collector, ok := a.stt.(metricsCollectorSTT); ok {
		providerUnsubscribes = append(providerUnsubscribes, collector.OnMetricsCollected(handler))
	}
	a.mu.Lock()
	a.metricsUnsubscribes = append(a.metricsUnsubscribes, providerUnsubscribes...)
	a.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			localUnsubscribe()
			for _, unsubscribe := range providerUnsubscribes {
				unsubscribe()
			}
		})
	}
}

func (a *StreamAdapter) OnError(handler STTErrorHandler) func() {
	return a.ErrorEmitter.OnError(handler)
}

func (a *StreamAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return a.stt.Recognize(ctx, frames, language)
}

type streamAdapterWrapper struct {
	adapter  *StreamAdapter
	ctx      context.Context
	cancel   context.CancelFunc
	language string

	eventCh chan *SpeechEvent
	errCh   chan error
	inputCh chan streamAdapterInput

	mu          sync.Mutex
	closed      bool
	inputClosed bool
	terminalErr error
	inputEnded  bool
	frameBuffer []*model.AudioFrame
	vadStream   vad.VADStream
	rateGuard   SampleRateGuard
	startOffset float64
	startTime   float64
	span        trace.Span
}

// StreamAdapterWrapper is kept public for LiveKit Agents API compatibility.
type StreamAdapterWrapper = streamAdapterWrapper

type streamAdapterInput struct {
	frame *model.AudioFrame
	flush bool
	end   bool
}

func (a *StreamAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	ctx, span := telemetry.NewSTTStreamSpan(ctx, Model(a.stt), Provider(a.stt))
	ctx, cancel := context.WithCancel(ctx)
	w := &streamAdapterWrapper{
		adapter:   a,
		ctx:       ctx,
		cancel:    cancel,
		language:  language,
		eventCh:   make(chan *SpeechEvent, 100),
		errCh:     make(chan error, 1),
		inputCh:   make(chan streamAdapterInput, 100),
		startTime: streamStartTimeNow(),
		span:      span,
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
	defer close(w.eventCh)
	defer w.span.End()

	vadStream, err := w.adapter.vad.Stream(w.ctx)
	if err != nil {
		logger.Logger.Errorw("Failed to start VAD stream in StreamAdapter", err)
		w.sendErr(err)
		return
	}
	if isNilVADStream(vadStream) {
		err := fmt.Errorf("nil VAD stream")
		logger.Logger.Errorw("Failed to start VAD stream in StreamAdapter", err)
		w.sendErr(err)
		return
	}
	w.mu.Lock()
	w.vadStream = vadStream
	w.mu.Unlock()
	defer w.markClosedFromRun()
	defer w.closeVADStream()

	// Goroutine to push frames to VAD and buffer them
	go func() {
		for {
			select {
			case <-w.ctx.Done():
				return
			case input, ok := <-w.inputCh:
				if !ok {
					return
				}
				if input.flush {
					if err := vadStream.Flush(); err != nil {
						w.sendErr(err)
						return
					}
					continue
				}
				if input.end {
					if err := vadStream.EndInput(); err != nil {
						w.sendErr(err)
					}
					return
				}
				w.mu.Lock()
				w.frameBuffer = append(w.frameBuffer, input.frame)
				w.mu.Unlock()
				if err := vadStream.PushFrame(input.frame); err != nil {
					w.mu.Lock()
					if n := len(w.frameBuffer); n > 0 && w.frameBuffer[n-1] == input.frame {
						w.frameBuffer = w.frameBuffer[:n-1]
					}
					w.mu.Unlock()
					w.sendErr(err)
					return
				}
			}
		}
	}()

	for {
		ev, err := vadStream.Next()
		if err != nil {
			if err != io.EOF && err != context.Canceled {
				logger.Logger.Errorw("VAD stream error in StreamAdapter", err)
			}
			w.sendErr(err)
			return
		}

		switch ev.Type {
		case vad.VADEventStartOfSpeech:
			w.eventCh <- &SpeechEvent{Type: SpeechEventStartOfSpeech}

			w.mu.Lock()
			if len(ev.Frames) > 0 {
				w.frameBuffer = make([]*model.AudioFrame, 0, len(ev.Frames))
				w.frameBuffer = append(w.frameBuffer, ev.Frames...)
			}
			w.mu.Unlock()

		case vad.VADEventEndOfSpeech:
			w.eventCh <- &SpeechEvent{Type: SpeechEventEndOfSpeech}

			w.mu.Lock()
			var frames []*model.AudioFrame
			if len(ev.Frames) > 0 {
				frames = ev.Frames // Use frames provided by VAD EndOfSpeech if available
			} else {
				// Otherwise use our accumulated buffer
				frames = make([]*model.AudioFrame, len(w.frameBuffer))
				copy(frames, w.frameBuffer)
			}
			w.frameBuffer = make([]*model.AudioFrame, 0)
			w.mu.Unlock()

			res, err := w.adapter.stt.Recognize(w.ctx, frames, w.language)
			if err == nil && res != nil && len(res.Alternatives) > 0 && res.Alternatives[0].Text != "" {
				w.mu.Lock()
				if !w.closed {
					w.eventCh <- &SpeechEvent{
						Type:         SpeechEventFinalTranscript,
						Alternatives: []SpeechData{res.Alternatives[0]},
					}
				}
				w.mu.Unlock()
			} else if err != nil {
				logger.Logger.Warnw("StreamAdapter STT Recognize failed", err)
				w.sendErr(err)
			}
		}
	}
}

func (w *streamAdapterWrapper) sendErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.terminalErr = err
	w.mu.Unlock()
	select {
	case w.errCh <- err:
	default:
	}
}

func (w *streamAdapterWrapper) StartTimeOffset() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startOffset
}

func (w *streamAdapterWrapper) SetStartTimeOffset(offset float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startOffset = nonNegativeStreamTime(offset)
}

func (w *streamAdapterWrapper) StartTime() float64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startTime
}

func (w *streamAdapterWrapper) SetStartTime(startTime float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startTime = nonNegativeStreamTime(startTime)
}

func (w *streamAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	if err := w.rateGuard.Check(frame); err != nil {
		w.mu.Unlock()
		return err
	}
	w.mu.Unlock()

	w.inputCh <- streamAdapterInput{frame: frame}
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	w.mu.Unlock()

	w.inputCh <- streamAdapterInput{flush: true}
	return nil
}

func (w *streamAdapterWrapper) EndInput() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	if w.inputEnded {
		w.mu.Unlock()
		return fmt.Errorf("stream input ended")
	}
	w.inputEnded = true
	w.mu.Unlock()

	w.inputCh <- streamAdapterInput{flush: true}
	w.inputCh <- streamAdapterInput{end: true}
	return nil
}

func (w *streamAdapterWrapper) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.closeInputLocked()
	w.mu.Unlock()

	w.cancel()
	return w.closeVADStream()
}

func (w *streamAdapterWrapper) markClosedFromRun() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.closeInputLocked()
	}
	w.mu.Unlock()
	w.cancel()
}

func (w *streamAdapterWrapper) closeInputLocked() {
	if !w.inputClosed {
		close(w.inputCh)
		w.inputClosed = true
	}
}

func (w *streamAdapterWrapper) closeVADStream() error {
	w.mu.Lock()
	vadStream := w.vadStream
	w.vadStream = nil
	w.mu.Unlock()
	if !isNilVADStream(vadStream) {
		return vadStream.Close()
	}
	return nil
}

func isNilVADStream(stream vad.VADStream) bool {
	if stream == nil {
		return true
	}
	value := reflect.ValueOf(stream)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (w *streamAdapterWrapper) Next() (*SpeechEvent, error) {
	select {
	case err := <-w.errCh:
		if err != nil {
			return nil, err
		}
	default:
	}

	select {
	case err := <-w.errCh:
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	case ev, ok := <-w.eventCh:
		if ok {
			return ev, nil
		}
		w.mu.Lock()
		err := w.terminalErr
		w.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	case <-w.ctx.Done():
		w.mu.Lock()
		err := w.terminalErr
		w.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, w.ctx.Err()
	}
}
