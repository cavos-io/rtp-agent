package vad

import (
	"context"
	"errors"
	"io"
	"math"
	"sync"

	"github.com/cavos-io/conversation-worker/model"
)

type SimpleVAD struct {
	options SimpleVADOptions
}

type SimpleVADOptions struct {
	Threshold             float64
	MinSpeechDuration     float64
	MinSilenceDuration    float64
	DeactivationThreshold float64
}

func NewSimpleVAD(threshold float64) *SimpleVAD {
	if threshold == 0 {
		threshold = 0.05
	}
	return NewSimpleVADWithOptions(SimpleVADOptions{Threshold: threshold})
}

func NewSimpleVADWithOptions(options SimpleVADOptions) *SimpleVAD {
	if options.Threshold == 0 {
		options.Threshold = 0.05
	}
	if options.DeactivationThreshold == 0 {
		options.DeactivationThreshold = options.Threshold
	}
	return &SimpleVAD{options: options}
}

func (v *SimpleVAD) Stream(ctx context.Context) (VADStream, error) {
	return &simpleVADStream{
		ctx:     ctx,
		options: v.options,
		events:  make(chan *VADEvent, 10),
	}, nil
}

type simpleVADStream struct {
	ctx                        context.Context
	options                    SimpleVADOptions
	events                     chan *VADEvent
	speaking                   bool
	closed                     bool
	inputEnded                 bool
	samplesIndex               int
	timestamp                  float64
	speechDuration             float64
	silenceDuration            float64
	accumulatedSpeechDuration  float64
	accumulatedSilenceDuration float64
	pendingSpeechFrames        []*model.AudioFrame
	speechFrames               []*model.AudioFrame
	mu                         sync.Mutex
}

func (s *simpleVADStream) PushFrame(frame *model.AudioFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}

	probability := frameRMS(frame)
	duration := frameDuration(frame)
	s.samplesIndex += int(frame.SamplesPerChannel)
	s.timestamp += duration

	inferenceSpeechDuration := s.speechDuration
	inferenceSilenceDuration := s.silenceDuration
	if s.speaking {
		inferenceSpeechDuration += duration
	} else {
		inferenceSilenceDuration += duration
	}
	s.events <- &VADEvent{
		Type:                  VADEventInferenceDone,
		SamplesIndex:          s.samplesIndex,
		Timestamp:             s.timestamp,
		SpeechDuration:        inferenceSpeechDuration,
		SilenceDuration:       inferenceSilenceDuration,
		Frames:                []*model.AudioFrame{frame},
		Probability:           probability,
		Speaking:              s.speaking,
		RawAccumulatedSpeech:  s.accumulatedSpeechDuration,
		RawAccumulatedSilence: s.accumulatedSilenceDuration,
	}

	if probability >= s.options.Threshold || (s.speaking && probability > s.options.DeactivationThreshold) {
		s.accumulatedSpeechDuration += duration
		s.accumulatedSilenceDuration = 0

		if !s.speaking {
			s.pendingSpeechFrames = append(s.pendingSpeechFrames, frame)
			if s.accumulatedSpeechDuration >= s.options.MinSpeechDuration {
				s.speaking = true
				s.speechDuration = s.accumulatedSpeechDuration
				s.silenceDuration = 0
				s.speechFrames = append([]*model.AudioFrame(nil), s.pendingSpeechFrames...)
				s.events <- &VADEvent{
					Type:           VADEventStartOfSpeech,
					SamplesIndex:   s.samplesIndex,
					Timestamp:      s.timestamp,
					SpeechDuration: s.speechDuration,
					Frames:         append([]*model.AudioFrame(nil), s.pendingSpeechFrames...),
					Speaking:       true,
				}
			}
		} else {
			s.speechDuration += duration
			s.silenceDuration = 0
			s.speechFrames = append(s.speechFrames, frame)
		}
	} else {
		s.accumulatedSilenceDuration += duration
		s.accumulatedSpeechDuration = 0

		if s.speaking {
			s.silenceDuration = s.accumulatedSilenceDuration
			if s.accumulatedSilenceDuration >= s.options.MinSilenceDuration {
				s.speaking = false
				frames := append([]*model.AudioFrame(nil), s.speechFrames...)
				s.events <- &VADEvent{
					Type:            VADEventEndOfSpeech,
					SamplesIndex:    s.samplesIndex,
					Timestamp:       s.timestamp,
					SpeechDuration:  s.speechDuration,
					SilenceDuration: s.silenceDuration,
					Frames:          frames,
					Speaking:        false,
				}
				s.resetSegment()
			}
		} else {
			s.silenceDuration += duration
			s.pendingSpeechFrames = nil
		}
	}

	return nil
}

func (s *simpleVADStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	s.resetSegment()
	return nil
}

func (s *simpleVADStream) EndInput() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("vad stream closed")
	}
	if s.inputEnded {
		return errors.New("vad stream input ended")
	}
	s.resetSegment()
	s.inputEnded = true
	s.closed = true
	close(s.events)
	return nil
}

func (s *simpleVADStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	return nil
}

func (s *simpleVADStream) Next() (*VADEvent, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case ev, ok := <-s.events:
		if !ok {
			return nil, io.EOF
		}
		return ev, nil
	}
}

func (s *simpleVADStream) resetSegment() {
	s.speaking = false
	s.speechDuration = 0
	s.silenceDuration = 0
	s.accumulatedSpeechDuration = 0
	s.accumulatedSilenceDuration = 0
	s.pendingSpeechFrames = nil
	s.speechFrames = nil
}

func frameRMS(frame *model.AudioFrame) float64 {
	sampleCount := len(frame.Data) / 2
	if sampleCount == 0 {
		return 0
	}

	var sum float64
	for i := 0; i < len(frame.Data); i += 2 {
		if i+1 >= len(frame.Data) {
			break
		}
		val := int16(uint16(frame.Data[i]) | uint16(frame.Data[i+1])<<8)
		fval := float64(val) / 32768.0
		sum += fval * fval
	}
	return math.Sqrt(sum / float64(sampleCount))
}

func frameDuration(frame *model.AudioFrame) float64 {
	if frame.SampleRate == 0 {
		return 0
	}
	return float64(frame.SamplesPerChannel) / float64(frame.SampleRate)
}
