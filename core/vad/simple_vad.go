package vad

import (
	"context"
	"math"
	"sync"

	"github.com/cavos-io/conversation-worker/model"
)

type SimpleVAD struct {
	Threshold float64
}

func NewSimpleVAD(threshold float64) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.05
	}
	return &SimpleVAD{Threshold: threshold}
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	return &simpleVADStream{
		ctx:       ctx,
		threshold: v.Threshold,
		events:    make(chan *VADEvent, 10),
	}, nil
}

type simpleVADStream struct {
	ctx           context.Context
	threshold     float64
	events        chan *VADEvent
	speaking      bool
	silenceFrames int
	buffered      []*model.AudioFrame
	mu            sync.Mutex
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

	if s.speaking {
		s.buffered = append(s.buffered, frame)
	}

	if rms > s.threshold {
		s.silenceFrames = 0
		if !s.speaking {
			s.speaking = true
			s.buffered = append(s.buffered, frame)
			s.events <- &VADEvent{Type: VADEventStartOfSpeech, Speaking: true}
		}
	} else {
		if s.speaking {
			s.silenceFrames++
			if s.silenceFrames > 25 { // ~500ms at 20ms per frame
				s.speaking = false
				s.silenceFrames = 0

				frames := make([]*model.AudioFrame, len(s.buffered))
				copy(frames, s.buffered)
				s.buffered = nil

				s.events <- &VADEvent{
					Type:     VADEventEndOfSpeech,
					Speaking: false,
					Frames:   frames,
				}
			}
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
