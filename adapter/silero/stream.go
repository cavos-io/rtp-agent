package silero

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

type sileroVADStream struct {
	ctx    context.Context
	inf    *inferencer
	events chan *vad.VADEvent
	mu     sync.Mutex
	closed bool

	speaking   bool
	sampleRate int

	threshold    float64
	negThreshold float64 // threshold - hysteresis, for speech-end detection

	minSpeechDuration  time.Duration
	minSilenceDuration float64 // seconds
	silenceSamples     int     // accumulated silence samples while triggered

	pendingSpeech   bool
	pendingSpeechAt time.Time
	speechStartTime time.Time

	pcmBuf       []float32
	speechFrames []*model.AudioFrame
}

func newSileroVADStream(ctx context.Context, inf *inferencer, opts VADOptions) *sileroVADStream {
	minDur := time.Duration(opts.MinSpeechDuration * float64(time.Second))
	return &sileroVADStream{
		ctx:                ctx,
		inf:                inf,
		events:             make(chan *vad.VADEvent, 20),
		sampleRate:         opts.SampleRate,
		threshold:          opts.ActivationThreshold,
		negThreshold:       opts.ActivationThreshold - hysteresis,
		minSpeechDuration:  minDur,
		minSilenceDuration: opts.MinSilenceDuration,
		pcmBuf:             make([]float32, 0, windowSize*4),
	}
}

func downsampleInt16(data []byte, srcRate, dstRate int) []byte {
	if srcRate <= dstRate {
		return data
	}
	factor := srcRate / dstRate
	numSamples := len(data) / 2
	outSamples := numSamples / factor
	out := make([]byte, outSamples*2)
	for i := 0; i < outSamples; i++ {
		srcIdx := i * factor * 2
		out[i*2] = data[srcIdx]
		out[i*2+1] = data[srcIdx+1]
	}
	return out
}

func int16BytesToFloat32(data []byte) []float32 {
	numSamples := len(data) / 2
	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(data[i*2]) | uint16(data[i*2+1])<<8)
		pcm[i] = float32(sample) / 32768.0
	}
	return pcm
}

func (s *sileroVADStream) speechDuration() float64 {
	if s.speechStartTime.IsZero() {
		return 0
	}
	return time.Since(s.speechStartTime).Seconds()
}

func (s *sileroVADStream) tryConfirmSpeechStart() {
	if !s.pendingSpeech || s.speaking {
		return
	}
	if s.minSpeechDuration > 0 && time.Since(s.pendingSpeechAt) < s.minSpeechDuration {
		return
	}
	s.speaking = true
	s.pendingSpeech = false
	s.speechStartTime = s.pendingSpeechAt
	dur := time.Since(s.pendingSpeechAt).Seconds()
	logger.Logger.Infow("Silero VAD: Speech START (confirmed)",
		"pendingSince", s.pendingSpeechAt.Format("15:04:05.000"),
		"accumulatedDuration", dur,
	)
	s.sendEvent(&vad.VADEvent{
		Type:           vad.VADEventStartOfSpeech,
		Speaking:       true,
		SpeechDuration: dur,
	})
}

func (s *sileroVADStream) cancelPendingSpeech() {
	if s.pendingSpeech {
		logger.Logger.Debugw("Silero VAD: pending speech cancelled (too short)")
		s.pendingSpeech = false
		s.pendingSpeechAt = time.Time{}
		s.speechFrames = nil
	}
}

func (s *sileroVADStream) emitSpeechEnd() {
	duration := s.speechDuration()
	frames := s.speechFrames
	s.speaking = false
	s.speechStartTime = time.Time{}
	s.speechFrames = nil
	logger.Logger.Infow("Silero VAD: Speech END",
		"duration", duration,
		"frames", len(frames),
	)
	s.sendEvent(&vad.VADEvent{
		Type:           vad.VADEventEndOfSpeech,
		Speaking:       false,
		SpeechDuration: duration,
		Frames:         frames,
	})
}

func (s *sileroVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	if s.speaking || s.pendingSpeech {
		s.speechFrames = append(s.speechFrames, frame)
	}

	audioData := frame.Data
	srcRate := int(frame.SampleRate)
	if srcRate > s.sampleRate && srcRate%s.sampleRate == 0 {
		audioData = downsampleInt16(audioData, srcRate, s.sampleRate)
	}

	samples := int16BytesToFloat32(audioData)
	s.pcmBuf = append(s.pcmBuf, samples...)

	for len(s.pcmBuf) >= windowSize {
		chunk := s.pcmBuf[:windowSize]

		inferStart := time.Now()
		prob, err := s.inf.infer(chunk)
		inferDur := time.Since(inferStart).Seconds()

		if err != nil {
			logger.Logger.Errorw("Silero VAD inference error", err)
			s.pcmBuf = s.pcmBuf[windowSize:]
			continue
		}

		speechProb := float64(prob)
		triggered := s.speaking || s.pendingSpeech

		if triggered {
			if speechProb >= s.negThreshold {
				s.silenceSamples = 0
			} else {
				s.silenceSamples += windowSize
				silenceDur := float64(s.silenceSamples) / float64(s.sampleRate)
				if silenceDur >= s.minSilenceDuration {
					if s.speaking {
						s.emitSpeechEnd()
					} else {
						s.cancelPendingSpeech()
					}
					s.silenceSamples = 0
				}
			}
		} else {
			if speechProb >= s.threshold {
				if s.minSpeechDuration > 0 {
					s.pendingSpeech = true
					s.pendingSpeechAt = time.Now()
					logger.Logger.Debugw("Silero VAD: speech pending (awaiting min duration)")
				} else {
					s.speaking = true
					s.speechStartTime = time.Now()
					s.sendEvent(&vad.VADEvent{
						Type:     vad.VADEventStartOfSpeech,
						Speaking: true,
					})
				}
			}
		}

		s.tryConfirmSpeechStart()

		s.sendEvent(&vad.VADEvent{
			Type:              vad.VADEventInferenceDone,
			Speaking:          s.speaking,
			Probability:       speechProb,
			InferenceDuration: inferDur,
			SpeechDuration:    s.speechDuration(),
		})

		s.pcmBuf = s.pcmBuf[windowSize:]
	}

	s.tryConfirmSpeechStart()

	return nil
}

func (s *sileroVADStream) sendEvent(ev *vad.VADEvent) {
	select {
	case <-s.ctx.Done():
	case s.events <- ev:
	default:
		logger.Logger.Warnw("Silero VAD event dropped (channel full)", nil)
	}
}

func (s *sileroVADStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	if s.speaking {
		s.emitSpeechEnd()
	}
	s.cancelPendingSpeech()
	s.pcmBuf = s.pcmBuf[:0]
	s.speechFrames = nil
	return nil
}

func (s *sileroVADStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	if s.speaking {
		s.emitSpeechEnd()
	}
	s.cancelPendingSpeech()

	s.closed = true
	close(s.events)

	if s.inf != nil {
		s.inf.destroy()
	}
	return nil
}

func (s *sileroVADStream) Next() (*vad.VADEvent, error) {
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
