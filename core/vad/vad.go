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
	Type                  VADEventType        `json:"type"`
	SamplesIndex          int                 `json:"samples_index"`
	Timestamp             float64             `json:"timestamp"`
	SpeechDuration        float64             `json:"speech_duration"`
	SilenceDuration       float64             `json:"silence_duration"`
	Frames                []*model.AudioFrame `json:"frames"`
	Probability           float64             `json:"probability"`
	InferenceDuration     float64             `json:"inference_duration"`
	Speaking              bool                `json:"speaking"`
	RawAccumulatedSilence float64             `json:"raw_accumulated_silence"`
	RawAccumulatedSpeech  float64             `json:"raw_accumulated_speech"`
}

type VADCapabilities struct {
	UpdateInterval float64 `json:"update_interval"`
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
