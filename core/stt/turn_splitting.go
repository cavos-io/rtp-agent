package stt

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

const (
	DefaultTurnSplitDelay = 450 * time.Millisecond
	turnSplitRetireDelay  = 5 * time.Second
)

// TurnSplittingSTT forces a fresh provider stream when a flushed turn does not
// produce a final transcript promptly. The logical stream continues forwarding
// late events from the retired provider stream.
type TurnSplittingSTT struct {
	inner STT
	delay time.Duration
}

func NewTurnSplittingSTT(inner STT, delay time.Duration) *TurnSplittingSTT {
	if delay <= 0 {
		delay = DefaultTurnSplitDelay
	}
	return &TurnSplittingSTT{inner: inner, delay: delay}
}

func (s *TurnSplittingSTT) Label() string                 { return s.inner.Label() }
func (s *TurnSplittingSTT) Capabilities() STTCapabilities { return s.inner.Capabilities() }
func (s *TurnSplittingSTT) Model() string                 { return Model(s.inner) }
func (s *TurnSplittingSTT) Provider() string              { return Provider(s.inner) }
func (s *TurnSplittingSTT) InputSampleRate() uint32       { return InputSampleRate(s.inner) }
func (s *TurnSplittingSTT) Prewarm()                      { Prewarm(s.inner) }
func (s *TurnSplittingSTT) Close() error                  { return Close(s.inner) }

func (s *TurnSplittingSTT) Recognize(ctx context.Context, frames []*model.AudioFrame, language string) (*SpeechEvent, error) {
	return s.inner.Recognize(ctx, frames, language)
}

func (s *TurnSplittingSTT) Stream(ctx context.Context, language string) (RecognizeStream, error) {
	stream, err := s.inner.Stream(ctx, language)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	w := &turnSplittingStream{
		ctx:      streamCtx,
		cancel:   cancel,
		provider: s.inner,
		language: language,
		delay:    s.delay,
		active:   stream,
		streams:  map[RecognizeStream]struct{}{stream: {}},
		events:   make(chan turnSplittingResult),
	}
	if timing, ok := stream.(StreamTiming); ok {
		w.startTimeOffset = timing.StartTimeOffset()
		w.startTime = timing.StartTime()
	}
	w.pump(stream)
	return w, nil
}

type turnSplittingStream struct {
	ctx      context.Context
	cancel   context.CancelFunc
	provider STT
	language string
	delay    time.Duration

	mu              sync.Mutex
	active          RecognizeStream
	streams         map[RecognizeStream]struct{}
	timer           *time.Timer
	pendingFinals   uint64
	finals          uint64
	splitting       bool
	closed          bool
	closeOnce       sync.Once
	events          chan turnSplittingResult
	startTimeOffset float64
	startTime       float64
}

type turnSplittingResult struct {
	event  *SpeechEvent
	err    error
	stream RecognizeStream
}

func (s *turnSplittingStream) StartTimeOffset() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTimeOffset
}

func (s *turnSplittingStream) SetStartTimeOffset(offset float64) {
	if offset < 0 {
		panic("start_time_offset must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTimeOffset = offset
	s.applyTiming(s.active)
}

func (s *turnSplittingStream) StartTime() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startTime
}

func (s *turnSplittingStream) SetStartTime(startTime float64) {
	if startTime < 0 {
		panic("start_time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startTime = startTime
	s.applyTiming(s.active)
}

func (s *turnSplittingStream) applyTiming(stream RecognizeStream) {
	timing, ok := stream.(StreamTiming)
	if !ok {
		return
	}
	SetStreamStartTimeOffset(timing, s.startTimeOffset)
	SetStreamStartTime(timing, s.startTime)
}

func (s *turnSplittingStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return io.ErrClosedPipe
	}
	return s.active.PushFrame(frame)
}

func (s *turnSplittingStream) Flush() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return io.ErrClosedPipe
	}
	err := s.active.Flush()
	if err == nil {
		s.pendingFinals = s.finals
		if s.timer != nil {
			s.timer.Stop()
		}
		snapshot := s.pendingFinals
		s.timer = time.AfterFunc(s.delay, func() { s.split(snapshot) })
	}
	s.mu.Unlock()
	return err
}

func (s *turnSplittingStream) Next() (*SpeechEvent, error) {
	for {
		if s.ctx.Err() != nil {
			return nil, io.EOF
		}
		select {
		case result := <-s.events:
			if result.err != nil {
				s.mu.Lock()
				active := s.active == result.stream && !s.closed
				s.mu.Unlock()
				if !active {
					continue
				}
			}
			if result.event == nil && result.err == nil {
				return nil, io.EOF
			}
			return result.event, result.err
		case <-s.ctx.Done():
			return nil, io.EOF
		}
	}
}

func (s *turnSplittingStream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		if s.timer != nil {
			s.timer.Stop()
		}
		streams := make([]RecognizeStream, 0, len(s.streams))
		for stream := range s.streams {
			streams = append(streams, stream)
		}
		clear(s.streams)
		s.mu.Unlock()

		s.cancel()
		for _, stream := range streams {
			closeErr = errors.Join(closeErr, stream.Close())
		}
	})
	return closeErr
}

func (s *turnSplittingStream) split(snapshot uint64) {
	s.mu.Lock()
	if s.closed || s.splitting || s.finals != snapshot || s.pendingFinals != snapshot {
		s.mu.Unlock()
		return
	}
	s.splitting = true
	s.mu.Unlock()

	replacement, err := s.provider.Stream(s.ctx, s.language)
	s.mu.Lock()
	s.splitting = false
	if err != nil {
		s.mu.Unlock()
		return
	}
	if s.closed || s.finals != snapshot || s.pendingFinals != snapshot {
		s.mu.Unlock()
		_ = replacement.Close()
		return
	}
	old := s.active
	s.applyTiming(replacement)
	s.active = replacement
	s.streams[replacement] = struct{}{}
	s.pendingFinals++
	s.pump(replacement)
	s.mu.Unlock()

	if ending, ok := old.(InputEnding); ok {
		_ = ending.EndInput()
		time.AfterFunc(turnSplitRetireDelay, func() { s.closeProviderStream(old) })
		return
	}
	s.closeProviderStream(old)
}

func (s *turnSplittingStream) closeProviderStream(stream RecognizeStream) {
	s.mu.Lock()
	_, owned := s.streams[stream]
	delete(s.streams, stream)
	s.mu.Unlock()
	if owned {
		_ = stream.Close()
	}
}

func (s *turnSplittingStream) pump(stream RecognizeStream) {
	go func() {
		defer s.closeProviderStream(stream)
		for {
			event, err := stream.Next()
			if err != nil {
				s.mu.Lock()
				active := s.active == stream && !s.closed
				s.mu.Unlock()
				if active && s.ctx.Err() == nil {
					select {
					case s.events <- turnSplittingResult{err: err, stream: stream}:
					case <-s.ctx.Done():
					}
				}
				return
			}
			if event != nil && event.Type == SpeechEventFinalTranscript {
				s.mu.Lock()
				if s.active == stream {
					s.finals++
					if s.timer != nil {
						s.timer.Stop()
					}
					s.pendingFinals = s.finals
				}
				s.mu.Unlock()
			}
			select {
			case s.events <- turnSplittingResult{event: event}:
			case <-s.ctx.Done():
				return
			}
		}
	}()
}
