package stt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/cavos-io/conversation-worker/core/vad"
	"github.com/cavos-io/conversation-worker/library/logger"
	"github.com/cavos-io/conversation-worker/model"
)

type FallbackAdapter struct {
	stts           []STT
	capabilities   STTCapabilities
	maxRetryPerSTT int
}

type FallbackAdapterOptions struct {
	MaxRetryPerSTT int
}

func NewFallbackAdapter(stts []STT) *FallbackAdapter {
	return NewFallbackAdapterWithOptions(stts, FallbackAdapterOptions{MaxRetryPerSTT: 1})
}

func NewFallbackAdapterWithOptions(stts []STT, options FallbackAdapterOptions) *FallbackAdapter {
	return newFallbackAdapter(stts, nil, options)
}

func NewFallbackAdapterWithVAD(stts []STT, vad vad.VAD) *FallbackAdapter {
	return newFallbackAdapter(stts, vad, FallbackAdapterOptions{MaxRetryPerSTT: 1})
}

func NewFallbackAdapterWithVADAndOptions(stts []STT, vad vad.VAD, options FallbackAdapterOptions) *FallbackAdapter {
	return newFallbackAdapter(stts, vad, options)
}

func newFallbackAdapter(stts []STT, vad vad.VAD, options FallbackAdapterOptions) *FallbackAdapter {
	if len(stts) == 0 {
		panic("FallbackAdapter requires at least one STT")
	}

	wrapped := make([]STT, len(stts))
	for i, stt := range stts {
		if !stt.Capabilities().Streaming {
			if vad == nil {
				panic(fmt.Sprintf("STT %q does not support streaming; provide a VAD or wrap it with StreamAdapter", stt.Label()))
			}
			wrapped[i] = NewStreamAdapter(stt, vad)
			continue
		}
		wrapped[i] = stt
	}

	capabilities := STTCapabilities{
		Streaming:      true,
		InterimResults: true,
		Diarization:    true,
	}
	if len(wrapped) > 0 {
		capabilities.AlignedTranscript = wrapped[0].Capabilities().AlignedTranscript
	}
	for _, stt := range wrapped {
		sttCapabilities := stt.Capabilities()
		capabilities.Streaming = capabilities.Streaming && sttCapabilities.Streaming
		capabilities.InterimResults = capabilities.InterimResults && sttCapabilities.InterimResults
		capabilities.Diarization = capabilities.Diarization && sttCapabilities.Diarization
		if sttCapabilities.AlignedTranscript == "" {
			capabilities.AlignedTranscript = ""
		}
		capabilities.OfflineRecognize = capabilities.OfflineRecognize || sttCapabilities.OfflineRecognize
	}

	return &FallbackAdapter{
		stts:           wrapped,
		capabilities:   capabilities,
		maxRetryPerSTT: options.MaxRetryPerSTT,
	}
}

func (f *FallbackAdapter) Label() string {
	return fmt.Sprintf("FallbackAdapter(%s)", f.stts[0].Label())
}

func (f *FallbackAdapter) Capabilities() STTCapabilities {
	return f.capabilities
}

func (f *FallbackAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	var lastErr error
	for i, stt := range f.stts {
		if i > 0 {
			logger.Logger.Infow("Falling back to next STT", "stt", stt.Label(), "previous_error", lastErr)
		}
		for attempt := 0; attempt <= f.maxRetryPerSTT; attempt++ {
			res, err := stt.Recognize(ctx, frames, language)
			if err == nil {
				return res, nil
			}
			lastErr = err
			if attempt < f.maxRetryPerSTT {
				logger.Logger.Warnw("Retrying STT recognize", err, "stt", stt.Label(), "attempt", attempt+1)
			}
		}
	}
	return nil, fmt.Errorf("all STTs failed, last error: %w", lastErr)
}

type fallbackRecognizeStream struct {
	adapter  *FallbackAdapter
	ctx      context.Context
	language string

	mu           sync.Mutex
	activeStream RecognizeStream
	activeIndex  int
	retries      map[int]int
	inputBuffer  []fallbackRecognizeInput

	eventCh chan *SpeechEvent
	errCh   chan error
	closeCh chan struct{}
	closed  bool
}

type fallbackRecognizeInput struct {
	frame *model.AudioFrame
	flush bool
}

func (f *FallbackAdapter) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	s := &fallbackRecognizeStream{
		adapter:     f,
		ctx:         ctx,
		language:    language,
		eventCh:     make(chan *SpeechEvent, 100),
		errCh:       make(chan error, 1),
		closeCh:     make(chan struct{}),
		inputBuffer: make([]fallbackRecognizeInput, 0),
		retries:     make(map[int]int),
	}

	if err := s.tryStartStream(0); err != nil {
		return nil, err
	}

	go s.monitorStream()

	return s, nil
}

func (s *fallbackRecognizeStream) tryStartStream(index int) error {
	var lastErr error
	for i := index; i < len(s.adapter.stts); i++ {
		for {
			stt := s.adapter.stts[i]
			stream, err := stt.Stream(s.ctx, s.language)
			if err != nil {
				logger.Logger.Errorw("Failed to start STT stream", err, "stt", stt.Label())
				lastErr = err
				if s.canRetrySTT(i) {
					s.retries[i]++
					continue
				}
				break
			}

			if err := s.replayBufferedFrames(stream); err != nil {
				stream.Close()
				lastErr = err
				if s.canRetrySTT(i) {
					s.retries[i]++
					continue
				}
				break
			}

			s.activeStream = stream
			s.activeIndex = i
			return nil
		}
	}

	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("all fallback STTs exhausted")
}

func (s *fallbackRecognizeStream) replayBufferedFrames(stream RecognizeStream) error {
	for _, input := range s.inputBuffer {
		if input.flush {
			if err := stream.Flush(); err != nil {
				return err
			}
			continue
		}
		if err := stream.PushFrame(input.frame); err != nil {
			return err
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
			if errors.Is(err, io.EOF) {
				s.errCh <- io.EOF
				return
			}

			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}

			// Try fallback
			logger.Logger.Warnw("STT stream failed, attempting fallback", err, "failed_stt", s.adapter.stts[s.activeIndex].Label())
			stream.Close()

			nextIndex := s.activeIndex + 1
			if s.canRetrySTT(s.activeIndex) {
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				s.errCh <- fbErr
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			continue
		}

		// Success, forward event
		if ev.Type == SpeechEventFinalTranscript {
			// Clear buffered input on final transcript since we don't need to replay previous turns.
			s.mu.Lock()
			s.inputBuffer = make([]fallbackRecognizeInput, 0)
			s.mu.Unlock()
		}

		select {
		case s.eventCh <- ev:
		case <-s.closeCh:
			return
		}
	}
}

func (s *fallbackRecognizeStream) canRetrySTT(index int) bool {
	if s.adapter.maxRetryPerSTT <= 0 {
		return false
	}
	if s.retries == nil {
		s.retries = make(map[int]int)
	}
	return s.retries[index] < s.adapter.maxRetryPerSTT
}

func (s *fallbackRecognizeStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("stream closed")
	}

	s.inputBuffer = append(s.inputBuffer, fallbackRecognizeInput{frame: frame})

	// Keep buffer reasonable (e.g., last 30 seconds at 100 frames/sec)
	if len(s.inputBuffer) > 3000 {
		s.inputBuffer = s.inputBuffer[len(s.inputBuffer)-3000:]
	}

	return s.activeStream.PushFrame(frame)
}

func (s *fallbackRecognizeStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	s.inputBuffer = append(s.inputBuffer, fallbackRecognizeInput{flush: true})
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
