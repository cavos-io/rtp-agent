package vad

import (
	"context"
	"encoding/json"
	"fmt"

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

func (e VADEvent) MarshalJSON() ([]byte, error) {
	type vadEventPayload VADEvent
	payload := vadEventPayload(e)
	if payload.Frames == nil {
		payload.Frames = []*model.AudioFrame{}
	}
	return json.Marshal(payload)
}

func (e *VADEvent) UnmarshalJSON(data []byte) error {
	if err := requireJSONFields(data, "vad event", "type", "samples_index", "timestamp", "speech_duration", "silence_duration"); err != nil {
		return err
	}

	type vadEventPayload VADEvent
	var payload vadEventPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*e = VADEvent(payload)
	if e.Frames == nil {
		e.Frames = []*model.AudioFrame{}
	}
	return nil
}

type VADCapabilities struct {
	UpdateInterval float64 `json:"update_interval"`
}

func (c *VADCapabilities) UnmarshalJSON(data []byte) error {
	if err := requireJSONFields(data, "vad capabilities", "update_interval"); err != nil {
		return err
	}

	type vadCapabilitiesPayload VADCapabilities
	var payload vadCapabilitiesPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	*c = VADCapabilities(payload)
	return nil
}

func requireJSONFields(data []byte, context string, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("%s %s is required", context, name)
		}
	}
	return nil
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
