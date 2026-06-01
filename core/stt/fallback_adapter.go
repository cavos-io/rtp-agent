package stt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
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

type FallbackAllFailedError struct {
	Count    int
	Labels   []string
	Duration time.Duration
	Err      error
}

func (e *FallbackAllFailedError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("all STTs failed (%v) after %s", e.Labels, e.Duration)
	}
	return fmt.Sprintf("all STTs failed (%v) after %s: %v", e.Labels, e.Duration, e.Err)
}

func (e *FallbackAllFailedError) Unwrap() error {
	return e.Err
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
		Streaming:        true,
		InterimResults:   true,
		Diarization:      true,
		OfflineRecognize: true,
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
	startedAt := time.Now()
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
	return nil, &FallbackAllFailedError{
		Count:    len(f.stts),
		Labels:   f.labels(),
		Duration: time.Since(startedAt),
		Err:      lastErr,
	}
}

func (f *FallbackAdapter) labels() []string {
	labels := make([]string, len(f.stts))
	for i, stt := range f.stts {
		labels[i] = stt.Label()
	}
	return labels
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
	recoveries   []RecognizeStream
	startedAt    time.Time
	lastErr      error

	eventCh chan *SpeechEvent
	errCh   chan error
	closeCh chan struct{}
	closed  bool
}

type fallbackRecognizeInput struct {
	frame *model.AudioFrame
	flush bool
	end   bool
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
		startedAt:   time.Now(),
		startTime:   streamStartTimeNow(),
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
		return s.allFailedError(lastErr)
	}
	return s.allFailedError(s.lastErr)
}

func (s *fallbackRecognizeStream) allFailedError(err error) error {
	return &FallbackAllFailedError{
		Count:    len(s.adapter.stts),
		Labels:   s.adapter.labels(),
		Duration: time.Since(s.startedAt),
		Err:      err,
	}
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
	s.startOffset = nonNegativeStreamTime(offset)
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
	s.startTime = nonNegativeStreamTime(startTime)
	s.applyTiming(s.activeStream)
}

func (s *fallbackRecognizeStream) applyTiming(stream RecognizeStream) {
	timing, ok := stream.(StreamTiming)
	if !ok {
		return
	}
	SetStreamStartTimeOffset(timing, s.startOffset)
	SetStreamStartTime(timing, s.startTime)
}

func replayFallbackInputs(stream RecognizeStream, inputs []fallbackRecognizeInput) error {
	for _, input := range inputs {
		if input.end {
			if ending, ok := stream.(InputEnding); ok {
				if err := ending.EndInput(); err != nil {
					return err
				}
			} else if err := stream.Flush(); err != nil {
				return err
			}
			continue
		}
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
			s.lastErr = err

			nextIndex := s.activeIndex + 1
			if s.canRetrySTT(s.activeIndex) {
				s.retries[s.activeIndex]++
				nextIndex = s.activeIndex
			} else {
				s.adapter.setAvailable(s.activeIndex, false)
				s.tryRecoverStream(s.activeIndex)
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

func (s *fallbackRecognizeStream) tryRecoverStream(index int) {
	if !s.adapter.markRecoveringStream(index) {
		return
	}
	recoveryCtx := context.WithoutCancel(s.ctx)

	go func() {
		defer s.adapter.clearRecoveringStream(index)

		stt := s.adapter.stts[index]
		stream, err := stt.Stream(recoveryCtx, s.language)
		if err != nil {
			return
		}
		defer stream.Close()

		s.mu.Lock()
		s.recoveries = append(s.recoveries, stream)
		s.mu.Unlock()
		defer s.removeRecovery(stream)

		for {
			ev, err := stream.Next()
			if err != nil {
				return
			}
			if ev.Type != SpeechEventFinalTranscript || len(ev.Alternatives) == 0 || ev.Alternatives[0].Text == "" {
				continue
			}
			s.adapter.setAvailable(index, true)
			logger.Logger.Infow("Recovered STT stream provider", "stt", stt.Label())
			return
		}
	}()
}

func (s *fallbackRecognizeStream) removeRecovery(stream RecognizeStream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, recovery := range s.recoveries {
		if recovery == stream {
			s.recoveries = append(s.recoveries[:i], s.recoveries[i+1:]...)
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

	for _, recovery := range s.recoveries {
		_ = recovery.PushFrame(frame)
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
	for _, recovery := range s.recoveries {
		_ = recovery.Flush()
	}
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
	s.inputBuffer = append(s.inputBuffer, fallbackRecognizeInput{flush: true}, fallbackRecognizeInput{end: true})
	for _, recovery := range s.recoveries {
		_ = recovery.Flush()
		if ending, ok := recovery.(InputEnding); ok {
			_ = ending.EndInput()
		} else {
			continue
		}
	}
	if err := s.activeStream.Flush(); err != nil {
		return err
	}
	if ending, ok := s.activeStream.(InputEnding); ok {
		return ending.EndInput()
	}
	return nil
}

func (s *fallbackRecognizeStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	activeStream := s.activeStream
	recoveries := append([]RecognizeStream(nil), s.recoveries...)
	s.recoveries = nil
	close(s.closeCh)
	s.mu.Unlock()

	for _, recovery := range recoveries {
		_ = recovery.Close()
	}
	return activeStream.Close()
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
