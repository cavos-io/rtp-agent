package vad

import (
	"context"

	"github.com/cavos-io/conversation-worker/model"
)

type VADEventType string

const (
	VADEventStartOfSpeech VADEventType = "start_of_speech"
	VADEventInferenceDone VADEventType = "inference_done"
	VADEventEndOfSpeech   VADEventType = "end_of_speech"
)

type VADEvent struct {
	Type                  VADEventType
	SamplesIndex          int
	Timestamp             float64
	SpeechDuration        float64
	SilenceDuration       float64
	Frames                []*model.AudioFrame
	Probability           float64
	InferenceDuration     float64
	Speaking              bool
	RawAccumulatedSilence float64
	RawAccumulatedSpeech  float64
}

type VAD interface {
	Stream(ctx context.Context) (VADStream, error)
}

type VADStream interface {
	PushFrame(frame *model.AudioFrame) error
	Flush() error
	Close() error
	Next() (*VADEvent, error)
}
