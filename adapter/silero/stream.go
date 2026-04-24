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
	ctx      context.Context
	detector *speech.Detector
	events   chan *vad.VADEvent
	mu       sync.Mutex
	closed   bool
	speaking bool
}

func newSileroVADStream(ctx context.Context, detector *speech.Detector) *sileroVADStream {
	return &sileroVADStream{
		ctx:      ctx,
		detector: detector,
		events:   make(chan *vad.VADEvent, 20),
	}
}

func (s *sileroVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	// Convert int16 PCM bytes to float32 samples (Silero expects float32)
	numSamples := len(frame.Data) / 2
	if numSamples == 0 {
		return nil
	}

	pcm := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sample := int16(uint16(frame.Data[i*2]) | uint16(frame.Data[i*2+1])<<8)
		pcm[i] = float32(sample) / 32768.0
	}

	// Run Silero ONNX inference
	segments, err := s.detector.Detect(pcm)
	if err != nil {
		logger.Logger.Errorw("Silero VAD detection error", err)
		return err
	}

	// Interpret segments: if any speech segments are returned, speech is active.
	// The Silero detector internally tracks state transitions and returns
	// segments with SpeechStartAt and SpeechEndAt timestamps.
	for _, seg := range segments {
		if seg.SpeechStartAt > 0 && !s.speaking {
			s.speaking = true
			logger.Logger.Debugw("Silero VAD: Speech START",
				"startAt", seg.SpeechStartAt,
			)
			s.sendEvent(&vad.VADEvent{
				Type:     vad.VADEventStartOfSpeech,
				Speaking: true,
			})
		}
		if seg.SpeechEndAt > 0 && s.speaking {
			s.speaking = false
			logger.Logger.Debugw("Silero VAD: Speech END",
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
		logger.Logger.Warnw("Silero VAD event dropped (channel full)")
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
