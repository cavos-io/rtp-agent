package vad

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/rtp-agent/model"
)

type SimpleVAD struct {
	Threshold float64
}

func NewSimpleVAD(threshold float64) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.0005
	}
	return &SimpleVAD{Threshold: threshold}
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	return &simpleVADStream{
		ctx:         ctx,
		threshold:   v.Threshold,
		events:      make(chan *VADEvent, 10),
		startFrames: 3,  // require 3 consecutive frames above threshold (~60ms at 20ms/frame)
		stopFrames:  50, // require 50 consecutive frames below threshold (~1s silence to stop)
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
	if c <= 3 || c%500 == 0 {
		fmt.Printf("🎚️  [VAD] Frame #%d: rms=%.6f threshold=%.4f speaking=%v above=%d below=%d\n", c, rms, s.threshold, s.speaking, s.aboveCount, s.belowCount)
	}

	if rms > s.threshold {
		s.aboveCount++
		s.belowCount = 0
		if !s.speaking && s.aboveCount >= s.startFrames {
			s.speaking = true
			fmt.Printf("🗣️  [VAD] Speech START at frame #%d (rms=%.6f, %d consecutive frames)\n", c, rms, s.aboveCount)
			s.events <- &VADEvent{Type: VADEventStartOfSpeech, Speaking: true}
		}
	} else {
		s.belowCount++
		s.aboveCount = 0
		if s.speaking && s.belowCount >= s.stopFrames {
			s.speaking = false
			fmt.Printf("🔇 [VAD] Speech END at frame #%d (rms=%.6f, %d consecutive silent frames)\n", c, rms, s.belowCount)
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
