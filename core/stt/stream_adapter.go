package stt

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
<<<<<<< HEAD
=======
	"github.com/cavos-io/rtp-agent/library/logger"
>>>>>>> origin/main
	"github.com/cavos-io/rtp-agent/model"
)

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

<<<<<<< HEAD
func (s *StreamAdapter) Label() string {
	return "stream_adapter(" + s.stt.Label() + ")"
=======
func (a *StreamAdapter) Label() string {
	return fmt.Sprintf("StreamAdapter(%s)", a.stt.Label())
>>>>>>> origin/main
}

func (s *StreamAdapter) Capabilities() STTCapabilities {
	return STTCapabilities{
		Streaming:        true,
		InterimResults:   false,
		Diarization:      false,
		OfflineRecognize: true,
	}
}

func (s *StreamAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return s.stt.Recognize(ctx, frames, language)
}

func (s *StreamAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	vadStream, err := s.vad.Stream(ctx)
	if err != nil {
		return nil, err
	}

	wrapper := &streamAdapterWrapper{
		adapter:   s,
		ctx:       ctx,
		language:  language,
		vadStream: vadStream,
		events:    make(chan *SpeechEvent, 10),
	}

	go wrapper.run()

	return wrapper, nil
}

type streamAdapterWrapper struct {
	adapter   *StreamAdapter
	ctx       context.Context
	language  string
	vadStream vad.VADStream
	events    chan *SpeechEvent
	closed    bool

<<<<<<< HEAD
	mu            sync.Mutex
	speechFrames  []*model.AudioFrame // accumulated frames while speaking
	isSpeaking    bool
=======
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
>>>>>>> origin/main
}

func (w *streamAdapterWrapper) PushFrame(frame *model.AudioFrame) error {
	w.mu.Lock()
	if w.isSpeaking {
		// deep copy the data so the frame buffer can be reused by the caller
		cp := &model.AudioFrame{
			Data:              append([]byte(nil), frame.Data...),
			SampleRate:        frame.SampleRate,
			NumChannels:       frame.NumChannels,
			SamplesPerChannel: frame.SamplesPerChannel,
		}
		w.speechFrames = append(w.speechFrames, cp)
	}
	w.mu.Unlock()
	return w.vadStream.PushFrame(frame)
}

func (w *streamAdapterWrapper) Flush() error {
	return w.vadStream.Flush()
}

func (w *streamAdapterWrapper) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.vadStream.Close()
	close(w.events)
	return nil
}

func (w *streamAdapterWrapper) Next() (*SpeechEvent, error) {
	select {
	case <-w.ctx.Done():
		return nil, w.ctx.Err()
	case ev, ok := <-w.events:
		if !ok {
			return nil, context.Canceled
		}
		return ev, nil
	}
}

func (w *streamAdapterWrapper) run() {
	defer close(w.events)

	eventCh := make(chan *vad.VADEvent)
	errCh := make(chan error, 1)

	go func() {
		for {
			vEvent, err := w.vadStream.Next()
			if err != nil {
				errCh <- err
				return
			}
			eventCh <- vEvent
		}
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case err := <-errCh:
			// Handle error if needed
			_ = err
			return
		case vEvent := <-eventCh:
			if vEvent.Type == vad.VADEventStartOfSpeech {
				w.mu.Lock()
				w.isSpeaking = true
				w.speechFrames = w.speechFrames[:0]
				w.mu.Unlock()
				w.events <- &SpeechEvent{Type: SpeechEventStartOfSpeech}
			} else if vEvent.Type == vad.VADEventEndOfSpeech {
				w.mu.Lock()
				w.isSpeaking = false
				frames := w.speechFrames
				w.speechFrames = nil
				w.mu.Unlock()

				w.events <- &SpeechEvent{Type: SpeechEventEndOfSpeech}

				if len(frames) > 0 {
					tEvent, err := w.adapter.stt.Recognize(w.ctx, frames, w.language)
					if err == nil && tEvent != nil && len(tEvent.Alternatives) > 0 && tEvent.Alternatives[0].Text != "" {
						w.events <- &SpeechEvent{
							Type:         SpeechEventFinalTranscript,
							Alternatives: []SpeechData{tEvent.Alternatives[0]},
						}
					}
				}
			}
		}
	}
}

