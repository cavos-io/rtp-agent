package silero

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	speech "github.com/streamer45/silero-vad-go/speech"
)

// sileroChunkSize is the number of float32 samples per Silero inference call.
// Silero VAD v5 requires exactly 512 samples at 16kHz (32ms window).
const sileroChunkSize = 512

type sileroVADStream struct {
	ctx        context.Context
	detector   *speech.Detector
	events     chan *vad.VADEvent
	mu         sync.Mutex
	closed     bool
	speaking   bool
	sampleRate int // Target sample rate for Silero (16000)

	// Accumulation buffer for PCM float32 samples
	pcmBuf []float32
}

func newSileroVADStream(ctx context.Context, detector *speech.Detector, sampleRate int) *sileroVADStream {
	return &sileroVADStream{
		ctx:        ctx,
		detector:   detector,
		events:     make(chan *vad.VADEvent, 20),
		sampleRate: sampleRate,
		pcmBuf:     make([]float32, 0, sileroChunkSize*2),
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

func (s *sileroVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
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

	// Process in chunks of sileroChunkSize (512 samples = 32ms at 16kHz)
	for len(s.pcmBuf) >= sileroChunkSize {
		chunk := s.pcmBuf[:sileroChunkSize]

		segments, err := s.detector.Detect(chunk)
		if err != nil {
			logger.Logger.Errorw("Silero VAD detection error", err)
			// Discard this chunk and continue
			s.pcmBuf = s.pcmBuf[sileroChunkSize:]
			continue
		}

		// Process speech segments
		for _, seg := range segments {
			if seg.SpeechStartAt > 0 && !s.speaking {
				s.speaking = true
				logger.Logger.Infow("Silero VAD: Speech START",
					"startAt", seg.SpeechStartAt,
				)
				s.sendEvent(&vad.VADEvent{
					Type:     vad.VADEventStartOfSpeech,
					Speaking: true,
				})
			}
			if seg.SpeechEndAt > 0 && s.speaking {
				s.speaking = false
				logger.Logger.Infow("Silero VAD: Speech END",
					"endAt", seg.SpeechEndAt,
				)
				s.sendEvent(&vad.VADEvent{
					Type:     vad.VADEventEndOfSpeech,
					Speaking: false,
				})
			}
		}

		// Advance buffer
		s.pcmBuf = s.pcmBuf[sileroChunkSize:]
	}

	return nil
}

func (s *sileroVADStream) sendEvent(ev *vad.VADEvent) {
	select {
	case <-s.ctx.Done():
	case s.events <- ev:
	default:
		// Drop event if channel is full to prevent blocking
		logger.Logger.Warnw("Silero VAD event dropped (channel full)", nil)
	}
}

func (s *sileroVADStream) Flush() error {
	return nil
}

func (s *sileroVADStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
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
