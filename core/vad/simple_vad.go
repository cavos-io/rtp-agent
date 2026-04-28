package vad

import (
	"context"
	"math"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

type SimpleVADOption func(*SimpleVAD)

func WithMinSilenceDuration(d float64) SimpleVADOption {
	return func(v *SimpleVAD) { v.minSilenceDuration = d }
}

func WithMinSpeechDuration(d float64) SimpleVADOption {
	return func(v *SimpleVAD) { v.minSpeechDuration = d }
}

type SimpleVAD struct {
	Threshold          float64
	minSilenceDuration float64
	minSpeechDuration  float64
}

func NewSimpleVAD(threshold float64, opts ...SimpleVADOption) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.0005
	}
	v := &SimpleVAD{Threshold: threshold}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

func (v *SimpleVAD) PreWarm() error {
	return nil // No warming needed for simple energy-based VAD
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	const frameDur = 0.02 // 20ms per frame

	stopFrames := 50 // default ~1s
	if v.minSilenceDuration > 0 {
		stopFrames = int(v.minSilenceDuration / frameDur)
		if stopFrames < 5 {
			stopFrames = 5
		}
	}

	startFrames := 3 // default ~60ms
	if v.minSpeechDuration > 0 {
		startFrames = int(v.minSpeechDuration / frameDur)
		if startFrames < 1 {
			startFrames = 1
		}
	}

	return &simpleVADStream{
		ctx:             ctx,
		threshold:       v.Threshold,
		events:          make(chan *VADEvent, 10),
		startFrames:     startFrames,
		stopFrames:      stopFrames,
		maxSpeechFrames: 500, // force EndOfSpeech after 10s of continuous speech (500 × 20ms)
	}, nil
}

type simpleVADStream struct {
	ctx         context.Context
	threshold   float64
	events      chan *VADEvent
	speaking    bool
	mu          sync.Mutex
	count       atomic.Int64
	aboveCount  int // consecutive frames above threshold
	belowCount  int // consecutive frames below threshold
	startFrames int // frames needed to start speaking (debounce)
	stopFrames  int // frames needed to stop speaking (debounce)

	// Max speech duration: force EndOfSpeech after this many frames of
	// continuous speech (prevents indefinite buffering from continuous audio).
	// At 20ms/frame: 500 frames = 10 seconds.
	speechFrames    int
	maxSpeechFrames int
}

func (s *simpleVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Simple RMS energy calculation
	var sum float64
	// Assume 16-bit PCM
	for i := 0; i < len(frame.Data); i += 2 {
		if i+1 >= len(frame.Data) {
			break
		}
		val := int16(uint16(frame.Data[i]) | uint16(frame.Data[i+1])<<8)
		fval := float64(val) / 32768.0
		sum += fval * fval
	}
	rms := math.Sqrt(sum / float64(len(frame.Data)/2))

	c := s.count.Add(1)
	logger.Logger.Debugw("VAD Process", "frame", c, "rms", rms, "threshold", s.threshold, "speaking", s.speaking)

	if rms > s.threshold {
		s.aboveCount++
		s.belowCount = 0
		if !s.speaking && s.aboveCount >= s.startFrames {
			s.speaking = true
			s.speechFrames = 0
			logger.Logger.Infow("VAD Speech START", "frame", c, "rms", rms, "aboveCount", s.aboveCount)
			s.events <- &VADEvent{Type: VADEventStartOfSpeech, Speaking: true}
		}
		if s.speaking {
			s.speechFrames++
			// Force EndOfSpeech after max duration to prevent indefinite buffering
			if s.maxSpeechFrames > 0 && s.speechFrames >= s.maxSpeechFrames {
				s.speaking = false
				s.speechFrames = 0
				logger.Logger.Infow("VAD Speech END (max duration)", "frame", c, "maxSpeechFrames", s.maxSpeechFrames)
				s.events <- &VADEvent{Type: VADEventEndOfSpeech, Speaking: false}
			}
		}
	} else {
		s.belowCount++
		s.aboveCount = 0
		if s.speaking {
			s.speechFrames++
		}
		if s.speaking && s.belowCount >= s.stopFrames {
			s.speaking = false
			s.speechFrames = 0
			logger.Logger.Infow("VAD Speech END", "frame", c, "rms", rms, "belowCount", s.belowCount)
			s.events <- &VADEvent{Type: VADEventEndOfSpeech, Speaking: false}
		}
	}

	return nil
}

func (s *simpleVADStream) Flush() error { return nil }
func (s *simpleVADStream) Close() error {
	close(s.events)
	return nil
}

func (s *simpleVADStream) Next() (*VADEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case ev, ok := <-s.events:
		if !ok {
			return nil, context.Canceled
		}
		return ev, nil
	}
}

