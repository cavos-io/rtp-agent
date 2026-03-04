package stt

import (
	"context"
	"fmt"
	"sync"

	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type FallbackAdapter struct {
	stts []STT
}

func NewFallbackAdapter(stts []STT) *FallbackAdapter {
	if len(stts) == 0 {
		panic("FallbackAdapter requires at least one STT")
	}
	return &FallbackAdapter{stts: stts}
}

func (f *FallbackAdapter) Label() string { 
	return fmt.Sprintf("FallbackAdapter(%s)", f.stts[0].Label()) 
}

func (f *FallbackAdapter) Capabilities() STTCapabilities {
	// Assume capabilities of the first (primary) STT
	return f.stts[0].Capabilities()
}

func (f *FallbackAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	var lastErr error
	for i, stt := range f.stts {
		if i > 0 {
			logger.Logger.Infow("Falling back to next STT", "stt", stt.Label(), "previous_error", lastErr)
		}
		res, err := stt.Recognize(ctx, frames, language)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all STTs failed, last error: %w", lastErr)
}

type fallbackRecognizeStream struct {
	adapter  *FallbackAdapter
	ctx      context.Context
	language string

	mu            sync.Mutex
	activeStream  RecognizeStream
	activeIndex   int
	frameBuffer   []*model.AudioFrame // Buffer to replay frames if fallback is needed
	
	eventCh       chan *SpeechEvent
	errCh         chan error
	closeCh       chan struct{}
	closed        bool
}

func (f *FallbackAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	s := &fallbackRecognizeStream{
		adapter:     f,
		ctx:         ctx,
		language:    language,
		eventCh:     make(chan *SpeechEvent, 100),
		errCh:       make(chan error, 1),
		closeCh:     make(chan struct{}),
		frameBuffer: make([]*model.AudioFrame, 0),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackRecognizeStream) tryStartStream(index int) error {
	if index >= len(s.adapter.stts) {
		return fmt.Errorf("all fallback STTs exhausted")
	}

	stt := s.adapter.stts[index]
	stream, err := stt.Stream(s.ctx, s.language)
	if err != nil {
		logger.Logger.Errorw("Failed to start STT stream", err, "stt", stt.Label())
		return s.tryStartStream(index + 1)
	}

	s.activeStream = stream
	s.activeIndex = index

	// Replay buffered frames
	for _, frame := range s.frameBuffer {
		if err := stream.PushFrame(frame); err != nil {
			stream.Close()
			return s.tryStartStream(index + 1)
		}
	}

	return nil
}

func (s *fallbackRecognizeStream) monitorStream() {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		stream := s.activeStream
		s.mu.Unlock()

		ev, err := stream.Next()
		if err != nil {
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			
			// Try fallback
			logger.Logger.Warnw("STT stream failed, attempting fallback", err, "failed_stt", s.adapter.stts[s.activeIndex].Label())
			stream.Close()
			
			if fbErr := s.tryStartStream(s.activeIndex + 1); fbErr != nil {
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		// Success, forward event
		if ev.Type == SpeechEventFinalTranscript {
			// Clear frame buffer on final transcript since we don't need to replay previous turns
			s.mu.Lock()
			s.frameBuffer = make([]*model.AudioFrame, 0)
			s.mu.Unlock()
		}

		select {
		case s.eventCh <- ev:
		case <-s.closeCh:
			return
		}
	}
}

func (s *fallbackRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("stream closed")
	}

	s.frameBuffer = append(s.frameBuffer, frame)
	
	// Keep buffer reasonable (e.g., last 30 seconds at 100 frames/sec)
	if len(s.frameBuffer) > 3000 {
		s.frameBuffer = s.frameBuffer[len(s.frameBuffer)-3000:]
	}

	return s.activeStream.PushFrame(frame)
}

func (s *fallbackRecognizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	return s.activeStream.Flush()
}

func (s *fallbackRecognizeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.closeCh)
	return s.activeStream.Close()
}

func (s *fallbackRecognizeStream) Next() (*SpeechEvent, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		return nil, context.Canceled
	}
}
