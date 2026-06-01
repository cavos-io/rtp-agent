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
	stts             []STT
	capabilities     STTCapabilities
	maxRetryPerSTT   int
	mu               sync.Mutex
	available        []bool
	recovering       []bool
	recoveringStream []bool
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
		stts:             wrapped,
		capabilities:     capabilities,
		maxRetryPerSTT:   options.MaxRetryPerSTT,
		available:        initialAvailability(len(wrapped)),
		recovering:       make([]bool, len(wrapped)),
		recoveringStream: make([]bool, len(wrapped)),
	}
}

func initialAvailability(size int) []bool {
	available := make([]bool, size)
	for i := range available {
		available[i] = true
	}
	return available
}

func (f *FallbackAdapter) Label() string {
	return fmt.Sprintf("FallbackAdapter(%s)", f.stts[0].Label())
}

func (f *FallbackAdapter) Capabilities() STTCapabilities {
	return f.capabilities
}

func (f *FallbackAdapter) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	var lastErr error
	allFailed := f.allUnavailable()
	for i, stt := range f.stts {
		if !allFailed && !f.isAvailable(i) {
			continue
		}
		if i > 0 {
			logger.Logger.Infow("Falling back to next STT", "stt", stt.Label(), "previous_error", lastErr)
		}
		for attempt := 0; attempt <= f.maxRetryPerSTT; attempt++ {
			res, err := stt.Recognize(ctx, frames, language)
			if err == nil {
				f.setAvailable(i, true)
				return res, nil
			}
			lastErr = err
			if attempt < f.maxRetryPerSTT {
				logger.Logger.Warnw("Retrying STT recognize", err, "stt", stt.Label(), "attempt", attempt+1)
			}
		}
		f.setAvailable(i, false)
		f.tryRecover(ctx, i, frames, language)
	}
	return nil, fmt.Errorf("all STTs failed, last error: %w", lastErr)
}

func (f *FallbackAdapter) tryRecover(ctx context.Context, index int, frames []*model.AudioFrame, language string) {
	if !f.markRecovering(index) {
		return
	}
	recoveryCtx := context.WithoutCancel(ctx)

	go func() {
		defer f.clearRecovering(index)

		stt := f.stts[index]
		for attempt := 0; attempt <= f.maxRetryPerSTT; attempt++ {
			if _, err := stt.Recognize(recoveryCtx, frames, language); err == nil {
				f.setAvailable(index, true)
				logger.Logger.Infow("Recovered STT provider", "stt", stt.Label())
				return
			}
		}
	}()
}

func (f *FallbackAdapter) allUnavailable() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, available := range f.available {
		if available {
			return false
		}
	}
	return true
}

func (f *FallbackAdapter) isAvailable(index int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.available) {
		return false
	}
	return f.available[index]
}

func (f *FallbackAdapter) setAvailable(index int, available bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.available) {
		return
	}
	f.available[index] = available
}

func (f *FallbackAdapter) markRecovering(index int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recovering) {
		return false
	}
	if f.recovering[index] {
		return false
	}
	f.recovering[index] = true
	return true
}

func (f *FallbackAdapter) clearRecovering(index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recovering) {
		return
	}
	f.recovering[index] = false
}

func (f *FallbackAdapter) tryRecoverStream(ctx context.Context, index int, language string, inputs []fallbackRecognizeInput) {
	if !f.markRecoveringStream(index) {
		return
	}
	recoveryCtx := context.WithoutCancel(ctx)

	go func() {
		defer f.clearRecoveringStream(index)

		stt := f.stts[index]
		stream, err := stt.Stream(recoveryCtx, language)
		if err != nil {
			return
		}
		defer stream.Close()

		if err := replayFallbackInputs(stream, inputs); err != nil {
			return
		}

		for {
			ev, err := stream.Next()
			if err != nil {
				return
			}
			if ev.Type != SpeechEventFinalTranscript || len(ev.Alternatives) == 0 || ev.Alternatives[0].Text == "" {
				continue
			}
			f.setAvailable(index, true)
			logger.Logger.Infow("Recovered STT stream provider", "stt", stt.Label())
			return
		}
	}()
}

func (f *FallbackAdapter) markRecoveringStream(index int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recoveringStream) {
		return false
	}
	if f.recoveringStream[index] {
		return false
	}
	f.recoveringStream[index] = true
	return true
}

func (f *FallbackAdapter) clearRecoveringStream(index int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recoveringStream) {
		return
	}
	f.recoveringStream[index] = false
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
	rateGuard    SampleRateGuard
	inputEnded   bool
	startOffset  float64
	startTime    float64

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
	allFailed := s.adapter.allUnavailable()
	for i := index; i < len(s.adapter.stts); i++ {
		if !allFailed && !s.adapter.isAvailable(i) {
			continue
		}
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
				s.adapter.setAvailable(i, false)
				break
			}

			if err := s.replayBufferedFrames(stream); err != nil {
				stream.Close()
				lastErr = err
				if s.canRetrySTT(i) {
					s.retries[i]++
					continue
				}
				s.adapter.setAvailable(i, false)
				break
			}

			s.applyTiming(stream)
			s.adapter.setAvailable(i, true)
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
	return replayFallbackInputs(stream, s.inputBuffer)
}

func (s *fallbackRecognizeStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startOffset
}

func (s *fallbackRecognizeStream) SetStartTimeOffset(offset float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startOffset = offset
	s.applyTiming(s.activeStream)
}

func (s *fallbackRecognizeStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTime
}

func (s *fallbackRecognizeStream) SetStartTime(startTime float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTime = startTime
	s.applyTiming(s.activeStream)
}

func (s *fallbackRecognizeStream) applyTiming(stream RecognizeStream) {
	timing, ok := stream.(StreamTiming)
	if !ok {
		return
	}
	timing.SetStartTimeOffset(s.startOffset)
	timing.SetStartTime(s.startTime)
}

func replayFallbackInputs(stream RecognizeStream, inputs []fallbackRecognizeInput) error {
	for _, input := range inputs {
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

func cloneFallbackInputs(inputs []fallbackRecognizeInput) []fallbackRecognizeInput {
	cloned := make([]fallbackRecognizeInput, len(inputs))
	copy(cloned, inputs)
	return cloned
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
			} else {
				s.adapter.setAvailable(s.activeIndex, false)
				s.adapter.tryRecoverStream(s.ctx, s.activeIndex, s.language, cloneFallbackInputs(s.inputBuffer))
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
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	if err := s.rateGuard.Check(frame); err != nil {
		return err
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
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	s.inputBuffer = append(s.inputBuffer, fallbackRecognizeInput{flush: true})
	return s.activeStream.Flush()
}

func (s *fallbackRecognizeStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream closed")
	}
	if s.inputEnded {
		return fmt.Errorf("stream input ended")
	}
	s.inputEnded = true
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
