package vad

import (
	"context"
	"math"
	"sync"
	"sync/atomic"

	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

type EnhancedVADOptions struct {
	ActivationThreshold float64
	MinSpeechDuration   float64 // ms
	MinSilenceDuration  float64 // ms
	SampleRate          int
}

type EnhancedVAD struct {
	Options EnhancedVADOptions
}

func NewEnhancedVAD(opts EnhancedVADOptions) *EnhancedVAD {
	if opts.ActivationThreshold == 0 {
		opts.ActivationThreshold = 0.5 // Default Silero threshold
	}
	if opts.MinSpeechDuration == 0 {
		opts.MinSpeechDuration = 250
	}
	if opts.MinSilenceDuration == 0 {
		opts.MinSilenceDuration = 1000
	}
	return &EnhancedVAD{Options: opts}
}

func (v *EnhancedVAD) Stream(ctx context.Context) (VADStream, error) {
	return &enhancedVADStream{
		ctx:          ctx,
		opts:         v.Options,
		events:       make(chan *VADEvent, 10),
		// Calculations based on 20ms frames (standard for WebRTC/Opus)
		startFrames:  int(v.Options.MinSpeechDuration / 20),
		stopFrames:   int(v.Options.MinSilenceDuration / 20),
	}, nil
}

type enhancedVADStream struct {
	ctx         context.Context
	opts        EnhancedVADOptions
	events      chan *VADEvent
	speaking    bool
	mu          sync.Mutex
	count       atomic.Int64
	aboveCount  int 
	belowCount  int
	startFrames int
	stopFrames  int

	// Energy moving average (exponential decay)
	smoothedEnergy float64
}

func (s *enhancedVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Calculate RMS Energy and Zero Crossing Rate
	var energy float64
	var zcr int
	numSamples := len(frame.Data) / 2
	if numSamples == 0 {
		return nil
	}

	lastVal := int16(0)
	for i := 0; i < len(frame.Data); i += 2 {
		val := int16(uint16(frame.Data[i]) | uint16(frame.Data[i+1])<<8)
		fval := float64(val) / 32768.0
		energy += fval * fval

		// Zero crossing detection
		if (val > 0 && lastVal < 0) || (val < 0 && lastVal > 0) {
			zcr++
		}
		lastVal = val
	}
	
	rms := math.Sqrt(energy / float64(numSamples))
	zcrRate := float64(zcr) / float64(numSamples)

	// 2. Exponential Moving Average for smoother transitions
	alpha := 0.3
	s.smoothedEnergy = (alpha * rms) + ((1 - alpha) * s.smoothedEnergy)

	// 3. Heuristic: Speech usually has ZCR between 0.05 and 0.25 (for 16kHz)
	// White noise usually has much higher ZCR (>0.3).
	// We use it as a secondary check to avoid triggering on loud white noise.
	isSpeechLike := s.smoothedEnergy > (s.opts.ActivationThreshold * 0.01) // Scale threshold
	if zcrRate > 0.4 {
		isSpeechLike = false // Likely noise
	}

	c := s.count.Add(1)

	if isSpeechLike {
		s.aboveCount++
		s.belowCount = 0
		if !s.speaking && s.aboveCount >= s.startFrames {
			s.speaking = true
			logger.Logger.Infow("Enhanced VAD START", "frame", c, "energy", s.smoothedEnergy, "zcr", zcrRate)
			s.events <- &VADEvent{Type: VADEventStartOfSpeech, Speaking: true}
		}
	} else {
		s.belowCount++
		s.aboveCount = 0
		if s.speaking && s.belowCount >= s.stopFrames {
			s.speaking = false
			logger.Logger.Infow("Enhanced VAD END", "frame", c, "belowCount", s.belowCount)
			s.events <- &VADEvent{Type: VADEventEndOfSpeech, Speaking: false}
		}
	}

	return nil
}

func (s *enhancedVADStream) Flush() error { return nil }
func (s *enhancedVADStream) Close() error {
	close(s.events)
	return nil
}

func (s *enhancedVADStream) Next() (*VADEvent, error) {
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
