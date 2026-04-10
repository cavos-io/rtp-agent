package stt

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
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
	return STTCapabilities{Streaming: true}
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
	audioCh chan *model.AudioFrame

	mu          sync.Mutex
	closed      bool
	frameBuffer []*model.AudioFrame
}

func (a *StreamAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	w := &streamAdapterWrapper{
		adapter:  a,
		ctx:      ctx,
		cancel:   cancel,
		language: language,
		eventCh:  make(chan *SpeechEvent, 100),
		audioCh:  make(chan *model.AudioFrame, 100),
	}

	go w.run()
	return w, nil
}

func (w *streamAdapterWrapper) run() {
	defer close(w.eventCh)

	vadStream, err := w.adapter.vad.Stream(w.ctx)
	if err != nil {
		fmt.Printf("❌ [STT-Adapter] Failed to start VAD stream: %v\n", err)
		logger.Logger.Errorw("Failed to start VAD stream in StreamAdapter", err)
		return
	}
	fmt.Println("✅ [STT-Adapter] Internal VAD stream started")

	// Goroutine to push frames to VAD and buffer them
	go func() {
		for {
			select {
			case <-w.ctx.Done():
				return
			case frame, ok := <-w.audioCh:
				if !ok {
					return
				}
				vadStream.PushFrame(frame)

				w.mu.Lock()
				w.frameBuffer = append(w.frameBuffer, frame)
				w.mu.Unlock()
			}
		}
	}()

	for {
		ev, err := vadStream.Next()
		if err != nil {
			if err != io.EOF && err != context.Canceled {
				fmt.Printf("❌ [STT-Adapter] VAD stream error: %v\n", err)
				logger.Logger.Errorw("VAD stream error in StreamAdapter", err)
			}
			return
		}

		switch ev.Type {
		case vad.VADEventStartOfSpeech:
			fmt.Println("🎙️ [STT-Adapter] VAD speech start → clearing buffer")
			w.eventCh <- &SpeechEvent{Type: SpeechEventStartOfSpeech}

			w.mu.Lock()
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
				frames = ev.Frames
			} else {
				frames = make([]*model.AudioFrame, len(w.frameBuffer))
				copy(frames, w.frameBuffer)
			}
			w.frameBuffer = make([]*model.AudioFrame, 0)
			w.mu.Unlock()

			fmt.Printf("🎙️ [STT-Adapter] VAD speech end → %d frames buffered, sending to Whisper\n", len(frames))

			if len(frames) > 0 {
				go func(f []*model.AudioFrame) {
					fmt.Printf("📤 [STT-Adapter] Calling Whisper Recognize with %d frames...\n", len(f))
					res, err := w.adapter.stt.Recognize(w.ctx, f, w.language)
					if err != nil {
						fmt.Printf("❌ [STT-Adapter] Whisper error: %v\n", err)
						logger.Logger.Warnw("StreamAdapter STT Recognize failed", err)
						return
					}
					if res == nil {
						fmt.Println("⚠️ [STT-Adapter] Whisper returned nil result")
						return
					}
					if len(res.Alternatives) == 0 || res.Alternatives[0].Text == "" {
						fmt.Println("⚠️ [STT-Adapter] Whisper returned empty transcript")
						return
					}
					fmt.Printf("✅ [STT-Adapter] Whisper transcript: '%s'\n", res.Alternatives[0].Text)
					res.Type = SpeechEventFinalTranscript

					w.mu.Lock()
					if !w.closed {
						w.eventCh <- res
					}
					w.mu.Unlock()
				}(frames)
			} else {
				fmt.Println("⚠️ [STT-Adapter] No frames buffered, skipping Whisper")
			}
		}
	}
}

func (w *streamAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
	w.mu.Unlock()

	w.audioCh <- frame
	return nil
}

func (w *streamAdapterWrapper) Flush() error {
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
	close(w.audioCh)
	return nil
}

func (w *streamAdapterWrapper) Next() (*SpeechEvent, error) {
	ev, ok := <-w.eventCh
	if !ok {
		return nil, context.Canceled
	}
	return ev, nil
}
