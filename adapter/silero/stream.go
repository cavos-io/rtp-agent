package silero

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
	speech "github.com/streamer45/silero-vad-go/speech"
)

type sileroVADStream struct {
	ctx        context.Context
	detector   *speech.Detector
	events     chan *vad.VADEvent
	mu         sync.Mutex
	closed     bool
	speaking   bool
	sampleRate int // Target sample rate for Silero (16000)
}

func newSileroVADStream(ctx context.Context, detector *speech.Detector, sampleRate int) *sileroVADStream {
	return &sileroVADStream{
		ctx:        ctx,
		detector:   detector,
		events:     make(chan *vad.VADEvent, 20),
		sampleRate: sampleRate,
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

	// Convert int16 PCM bytes to float32 samples (Silero expects float32)
	numSamples := len(audioData) / 2
	if numSamples == 0 {
		return nil
	}

	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(audioData[i*2]) | uint16(audioData[i*2+1])<<8)
		pcm[i] = float32(sample) / 32768.0
	}

	// Run Silero ONNX inference
	segments, err := s.detector.Detect(pcm)
	if err != nil {
		logger.Logger.Errorw("Silero VAD detection error", err)
		return err
	}

	// Interpret segments: if any speech segments are returned, speech is active.
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
