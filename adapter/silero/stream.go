package silero

import (
	"context"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	speech "github.com/streamer45/silero-vad-go/speech"
)

// sileroChunkSize is the number of float32 samples to accumulate before
// calling Silero Detect(). The internal loop processes in 512-sample windows
// using `i < len(pcm)-windowSize`, so we need more than 512 samples.
// 1536 = 3 × 512, giving 2 full inference windows per Detect() call (96ms at 16kHz).
const sileroChunkSize = 1536

type sileroVADStream struct {
	ctx        context.Context
	detector   *speech.Detector
	events     chan *vad.VADEvent
	mu         sync.Mutex
	closed     bool
	speaking   bool
	sampleRate int // Target sample rate for Silero (16000)

	// MinSpeechDuration filtering: hold back speech-start until speech
	// has been ongoing for at least this duration. Prevents false triggers
	// from brief noise spikes. Mirrors Python Silero VAD behavior.
	minSpeechDuration time.Duration
	pendingSpeech     bool      // Silero detected speech but hasn't reached min duration yet
	pendingSpeechAt   time.Time // When Silero first detected speech in this pending window

	speechStartTime time.Time

	// Accumulation buffer for PCM float32 samples
	pcmBuf []float32

	// Audio frames buffer: accumulates frames during speech for END_OF_SPEECH event
	speechFrames []*model.AudioFrame
}

func newSileroVADStream(ctx context.Context, detector *speech.Detector, sampleRate int, minSpeechDurationSec float64) *sileroVADStream {
	minDur := time.Duration(minSpeechDurationSec * float64(time.Second))
	return &sileroVADStream{
		ctx:               ctx,
		detector:          detector,
		events:            make(chan *vad.VADEvent, 20),
		sampleRate:        sampleRate,
		minSpeechDuration: minDur,
		pcmBuf:            make([]float32, 0, sileroChunkSize*2),
	}
}

// downsampleInt16 performs simple decimation from srcRate to dstRate.
// For 48kHz → 16kHz this means taking every 3rd sample (factor of 3).
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

// int16BytesToFloat32 converts little-endian int16 PCM bytes to float32 in [-1, 1].
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

// tryConfirmSpeechStart checks if pending speech has exceeded MinSpeechDuration.
// If so, emits the speech-start event and transitions to speaking state.
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

	// Buffer frame for END_OF_SPEECH if currently speaking or pending
	if s.speaking || s.pendingSpeech {
		s.speechFrames = append(s.speechFrames, frame)
	}

	// Downsample if needed (e.g., 48kHz → 16kHz)
	audioData := frame.Data
	srcRate := int(frame.SampleRate)
	if srcRate > s.sampleRate && srcRate%s.sampleRate == 0 {
		audioData = downsampleInt16(audioData, srcRate, s.sampleRate)
	}

	// Convert to float32 and accumulate
	samples := int16BytesToFloat32(audioData)
	s.pcmBuf = append(s.pcmBuf, samples...)

	// Process in chunks of sileroChunkSize
	for len(s.pcmBuf) >= sileroChunkSize {
		chunk := s.pcmBuf[:sileroChunkSize]

		inferStart := time.Now()
		segments, err := s.detector.Detect(chunk)
		inferDur := time.Since(inferStart).Seconds()

		if err != nil {
			if err.Error() == "unexpected speech end" {
				if s.speaking {
					s.emitSpeechEnd()
				} else {
					s.cancelPendingSpeech()
				}
			} else {
				logger.Logger.Errorw("Silero VAD detection error", err)
			}
			s.pcmBuf = s.pcmBuf[sileroChunkSize:]
			continue
		}

		speechDetected := false
		for _, seg := range segments {
			hasEnd := seg.SpeechEndAt > 0

			if hasEnd {
				if s.speaking {
					s.emitSpeechEnd()
				} else if s.pendingSpeech {
					s.cancelPendingSpeech()
				}
			} else {
				speechDetected = true
				if !s.speaking && !s.pendingSpeech {
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
		}

		// Check if pending speech has reached min duration
		s.tryConfirmSpeechStart()

		// Emit INFERENCE_DONE after each Detect() call
		// silero-vad-go doesn't expose raw probability, so we use binary approximation
		prob := 0.0
		if s.speaking || s.pendingSpeech || speechDetected {
			prob = 1.0
		}
		s.sendEvent(&vad.VADEvent{
			Type:              vad.VADEventInferenceDone,
			Speaking:          s.speaking,
			Probability:       prob,
			InferenceDuration: inferDur,
			SpeechDuration:    s.speechDuration(),
		})

		s.pcmBuf = s.pcmBuf[sileroChunkSize:]
	}

	// Also check pending speech between chunks (in case no segments)
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

	if s.detector != nil {
		return s.detector.Destroy()
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
