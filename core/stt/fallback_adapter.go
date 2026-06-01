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
	stts                 []STT
	capabilities         STTCapabilities
	maxRetryPerSTT       int
	attemptTimeout       time.Duration
	retryInterval        time.Duration
	mu                   sync.Mutex
	available            []bool
	recovering           []bool
	recoveryCancels      []context.CancelFunc
	recoveringStream     []bool
	recoveryStreams      []RecognizeStream
	availabilityHandlers []availabilityHandlerSubscription
	nextAvailabilityID   uint64
}

const (
	defaultFallbackAttemptTimeout = 10 * time.Second
	defaultFallbackRetryInterval  = 5 * time.Second
)

type FallbackAdapterOptions struct {
	MaxRetryPerSTT int
	AttemptTimeout time.Duration
	RetryInterval  time.Duration
}

type AvailabilityChangedEvent struct {
	STT       STT
	Available bool
}

type AvailabilityChangedHandler func(AvailabilityChangedEvent)

type availabilityHandlerSubscription struct {
	id      uint64
	handler AvailabilityChangedHandler
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
	return NewFallbackAdapterWithOptions(stts, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
		RetryInterval:  defaultFallbackRetryInterval,
	})
}

func NewFallbackAdapterWithOptions(stts []STT, options FallbackAdapterOptions) *FallbackAdapter {
	return newFallbackAdapter(stts, nil, options)
}

func NewFallbackAdapterWithVAD(stts []STT, vad vad.VAD) *FallbackAdapter {
	return newFallbackAdapter(stts, vad, FallbackAdapterOptions{
		MaxRetryPerSTT: 1,
		RetryInterval:  defaultFallbackRetryInterval,
	})
}

func NewFallbackAdapterWithVADAndOptions(stts []STT, vad vad.VAD, options FallbackAdapterOptions) *FallbackAdapter {
	return newFallbackAdapter(stts, vad, options)
}

func newFallbackAdapter(stts []STT, vad vad.VAD, options FallbackAdapterOptions) *FallbackAdapter {
	if len(stts) == 0 {
		panic("FallbackAdapter requires at least one STT")
	}
	if options.AttemptTimeout <= 0 {
		options.AttemptTimeout = defaultFallbackAttemptTimeout
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
		attemptTimeout:   options.AttemptTimeout,
		retryInterval:    options.RetryInterval,
		available:        initialAvailability(len(wrapped)),
		recovering:       make([]bool, len(wrapped)),
		recoveryCancels:  make([]context.CancelFunc, len(wrapped)),
		recoveringStream: make([]bool, len(wrapped)),
		recoveryStreams:  make([]RecognizeStream, len(wrapped)),
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

func (f *FallbackAdapter) Model() string {
	return "FallbackAdapter"
}

func (f *FallbackAdapter) Provider() string {
	return "livekit"
}

func (f *FallbackAdapter) Prewarm() {
	Prewarm(f.stts[0])
}

func (f *FallbackAdapter) Capabilities() STTCapabilities {
	return f.capabilities
}

func (f *FallbackAdapter) Close() error {
	f.mu.Lock()
	cancels := append([]context.CancelFunc(nil), f.recoveryCancels...)
	for i := range f.recoveryCancels {
		f.recoveryCancels[i] = nil
	}
	streams := append([]RecognizeStream(nil), f.recoveryStreams...)
	for i := range f.recoveryStreams {
		f.recoveryStreams[i] = nil
	}
	f.mu.Unlock()

	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
	closeStreams(streams)
	return nil
}

func (f *FallbackAdapter) OnAvailabilityChanged(handler AvailabilityChangedHandler) func() {
	if handler == nil {
		return func() {}
	}
	f.mu.Lock()
	f.nextAvailabilityID++
	id := f.nextAvailabilityID
	f.availabilityHandlers = append(f.availabilityHandlers, availabilityHandlerSubscription{
		id:      id,
		handler: handler,
	})
	f.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			f.removeAvailabilityChangedHandler(id)
		})
	}
}

func (f *FallbackAdapter) removeAvailabilityChangedHandler(id uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, subscription := range f.availabilityHandlers {
		if subscription.id == id {
			f.availabilityHandlers = append(f.availabilityHandlers[:i], f.availabilityHandlers[i+1:]...)
			return
		}
	}
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
			res, err := f.recognizeAttempt(ctx, stt, frames, language)
			if err == nil {
				f.setAvailable(i, true)
				return res, nil
			}
			lastErr = err
			if attempt < f.maxRetryPerSTT {
				logger.Logger.Warnw("Retrying STT recognize", err, "stt", stt.Label(), "attempt", attempt+1)
				if err := f.waitRetryInterval(ctx); err != nil {
					lastErr = err
					break
				}
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
	recoveryCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	f.setRecoveryCancel(index, cancel)

	go func() {
		defer cancel()
		defer f.setRecoveryCancel(index, nil)
		defer f.clearRecovering(index)

		stt := f.stts[index]
		for attempt := 0; attempt <= f.maxRetryPerSTT; attempt++ {
			if _, err := f.recognizeAttempt(recoveryCtx, stt, frames, language); err == nil {
				f.setAvailable(index, true)
				logger.Logger.Infow("Recovered STT provider", "stt", stt.Label())
				return
			}
			if attempt < f.maxRetryPerSTT {
				if err := f.waitRetryInterval(recoveryCtx); err != nil {
					return
				}
			}
		}
	}()
}

func (f *FallbackAdapter) setRecoveryCancel(index int, cancel context.CancelFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recoveryCancels) {
		return
	}
	f.recoveryCancels[index] = cancel
}

func (f *FallbackAdapter) waitRetryInterval(ctx context.Context) error {
	if f.retryInterval <= 0 {
		return nil
	}
	timer := time.NewTimer(f.retryInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *FallbackAdapter) recognizeAttempt(ctx context.Context, stt STT, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	attemptCtx := ctx
	var cancel context.CancelFunc
	if f.attemptTimeout > 0 {
		attemptCtx, cancel = context.WithTimeout(ctx, f.attemptTimeout)
		defer cancel()
	}
	return stt.Recognize(attemptCtx, frames, language)
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
	if index < 0 || index >= len(f.available) {
		f.mu.Unlock()
		return
	}
	if f.available[index] == available {
		f.mu.Unlock()
		return
	}
	f.available[index] = available
	event := AvailabilityChangedEvent{STT: f.stts[index], Available: available}
	subscriptions := append([]availabilityHandlerSubscription(nil), f.availabilityHandlers...)
	f.mu.Unlock()

	for _, subscription := range subscriptions {
		subscription.handler(event)
	}
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

	eventCh     chan *SpeechEvent
	errCh       chan error
	closeCh     chan struct{}
	closed      bool
	terminalErr error
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
		s.closeRecoveries()
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
			s.tryRecoverStream(i)
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
					if err := s.adapter.waitRetryInterval(s.ctx); err != nil {
						lastErr = err
						break
					}
					continue
				}
				s.adapter.setAvailable(i, false)
				s.tryRecoverStream(i)
				break
			}

			if err := s.replayBufferedFrames(stream); err != nil {
				stream.Close()
				lastErr = err
				if s.canRetrySTT(i) {
					s.retries[i]++
					if err := s.adapter.waitRetryInterval(s.ctx); err != nil {
						lastErr = err
						break
					}
					continue
				}
				s.adapter.setAvailable(i, false)
				s.tryRecoverStream(i)
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

		ev, err := s.nextActiveStreamEvent(stream)
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = stream.Close()
				s.finish(io.EOF)
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
				if retryErr := s.adapter.waitRetryInterval(s.ctx); retryErr != nil {
					recoveries := s.detachRecoveriesLocked()
					s.closed = true
					s.terminalErr = retryErr
					close(s.closeCh)
					s.mu.Unlock()
					closeStreams(recoveries)
					return
				}
			} else {
				s.adapter.setAvailable(s.activeIndex, false)
				s.tryRecoverStream(s.activeIndex)
			}

			if fbErr := s.tryStartStream(nextIndex); fbErr != nil {
				recoveries := s.detachRecoveriesLocked()
				s.closed = true
				s.terminalErr = fbErr
				close(s.closeCh)
				s.mu.Unlock()
				closeStreams(recoveries)
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

func (s *fallbackRecognizeStream) nextActiveStreamEvent(stream RecognizeStream) (*SpeechEvent, error) {
	if s.adapter.attemptTimeout <= 0 {
		return stream.Next()
	}

	type nextResult struct {
		event *SpeechEvent
		err   error
	}
	resultCh := make(chan nextResult, 1)
	go func() {
		event, err := stream.Next()
		resultCh <- nextResult{event: event, err: err}
	}()

	timer := time.NewTimer(s.adapter.attemptTimeout)
	defer timer.Stop()

	select {
	case result := <-resultCh:
		return result.event, result.err
	case <-timer.C:
		_ = stream.Close()
		return nil, context.DeadlineExceeded
	case <-s.closeCh:
		return nil, context.Canceled
	}
}

func (s *fallbackRecognizeStream) finish(err error) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.terminalErr = err
		close(s.closeCh)
	}
	s.mu.Unlock()
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
	stt := s.adapter.stts[index]
	stream, err := stt.Stream(recoveryCtx, s.language)
	if err != nil || stream == nil {
		s.adapter.clearRecoveringStream(index)
		return
	}

	s.adapter.setRecoveryStream(index, stream)
	s.recoveries = append(s.recoveries, stream)

	go func() {
		defer s.adapter.clearRecoveringStream(index)
		defer s.adapter.setRecoveryStream(index, nil)
		defer stream.Close()
		defer s.removeRecovery(stream)

		for {
			ev, err := s.nextRecoveryStreamEvent(stream)
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

func (s *fallbackRecognizeStream) nextRecoveryStreamEvent(stream RecognizeStream) (*SpeechEvent, error) {
	if s.adapter.attemptTimeout <= 0 {
		return stream.Next()
	}

	type nextResult struct {
		event *SpeechEvent
		err   error
	}
	resultCh := make(chan nextResult, 1)
	go func() {
		event, err := stream.Next()
		resultCh <- nextResult{event: event, err: err}
	}()

	timer := time.NewTimer(s.adapter.attemptTimeout)
	defer timer.Stop()

	select {
	case result := <-resultCh:
		return result.event, result.err
	case <-timer.C:
		_ = stream.Close()
		return nil, context.DeadlineExceeded
	}
}

func (f *FallbackAdapter) setRecoveryStream(index int, stream RecognizeStream) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.recoveryStreams) {
		return
	}
	f.recoveryStreams[index] = stream
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
	recoveries := s.detachRecoveriesLocked()
	close(s.closeCh)
	s.mu.Unlock()

	closeStreams(recoveries)
	return activeStream.Close()
}

func (s *fallbackRecognizeStream) closeRecoveries() {
	s.mu.Lock()
	recoveries := s.detachRecoveriesLocked()
	s.mu.Unlock()
	closeStreams(recoveries)
}

func (s *fallbackRecognizeStream) detachRecoveriesLocked() []RecognizeStream {
	recoveries := append([]RecognizeStream(nil), s.recoveries...)
	s.recoveries = nil
	return recoveries
}

func closeStreams(streams []RecognizeStream) {
	for _, stream := range streams {
		if stream == nil {
			continue
		}
		_ = stream.Close()
	}
}

func (s *fallbackRecognizeStream) Next() (*SpeechEvent, error) {
	select {
	case ev := <-s.eventCh:
		return ev, nil
	case err := <-s.errCh:
		return nil, err
	case <-s.closeCh:
		s.mu.Lock()
		err := s.terminalErr
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}
}
