package stt

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
)

// StreamAdapter converts a non-streaming STT into a streaming STT by coupling it with a VAD.
// It buffers audio frames and sends them to the underlying STT Recognize method when the VAD detects speech.
type StreamAdapter struct {
	stt STT
	vad vad.VAD
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
	inputEnded  bool
	frameBuffer []*model.AudioFrame
	vadStream   vad.VADStream
	rateGuard   SampleRateGuard
	startOffset float64
	startTime   float64
}

type streamAdapterInput struct {
	frame *model.AudioFrame
	flush bool
	end   bool
}

func (a *StreamAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
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
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
	defer close(w.eventCh)

	vadStream, err := w.adapter.vad.Stream(w.ctx)
	if err != nil {
		logger.Logger.Errorw("Failed to start VAD stream in StreamAdapter", err)
		w.sendErr(err)
		return
	}
	w.mu.Lock()
	w.vadStream = vadStream
	w.mu.Unlock()
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
					if err := vadStream.Flush(); err != nil {
						w.sendErr(err)
						return
					}
					if err := vadStream.EndInput(); err != nil {
						w.sendErr(err)
					}
					return
				}
				if err := vadStream.PushFrame(input.frame); err != nil {
					w.sendErr(err)
					return
				}

				w.mu.Lock()
				w.frameBuffer = append(w.frameBuffer, input.frame)
				w.mu.Unlock()
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

			// Clear old frames that are before the speech start
			w.mu.Lock()
			// We can optionally keep some padding before speech started, but for simplicity we'll just clear
			// We'll rely on the VAD event to tell us the actual start frame, or we just keep accumulating
			// For this implementation, we just start fresh on speech start to avoid huge buffers.
			// Wait, the VAD event might give us the frames.
			w.frameBuffer = make([]*model.AudioFrame, 0)
			if len(ev.Frames) > 0 {
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

			if len(frames) > 0 {
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
}

func (w *streamAdapterWrapper) sendErr(err error) {
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
	w.mu.Unlock()

	w.cancel()
	close(w.inputCh)
	return w.closeVADStream()
}

func (w *streamAdapterWrapper) closeVADStream() error {
	w.mu.Lock()
	vadStream := w.vadStream
	w.vadStream = nil
	w.mu.Unlock()
	if vadStream != nil {
		return vadStream.Close()
	}
	return nil
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
		return nil, context.Canceled
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	}
}
