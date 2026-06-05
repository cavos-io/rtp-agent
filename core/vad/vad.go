package vad

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

type VADCapabilities struct {
	UpdateInterval float64
}

type VADMetricsHandler func(*telemetry.VADMetrics)

type VAD interface {
	Label() string
	Model() string
	Provider() string
	Capabilities() VADCapabilities
	OnMetricsCollected(handler VADMetricsHandler) func()
	Stream(ctx context.Context) (VADStream, error)
}

type VADStream interface {
	PushFrame(frame *model.AudioFrame) error
	Flush() error
	EndInput() error
	Close() error
	Next() (*VADEvent, error)
}
