package main

import (
	"encoding/json"
	"fmt"
	"strings"

	lkvad "github.com/cavos-io/rtp-agent/core/vad"
)

func runVADValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action         string   `json:"action"`
		UpdateInterval *float64 `json:"update_interval"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "capabilities_json"
	}
	updateInterval := 0.5
	if payload.UpdateInterval != nil {
		updateInterval = *payload.UpdateInterval
	}

	switch payload.Action {
	case "capabilities_json":
		data, err := json.Marshal(lkvad.VADCapabilities{UpdateInterval: updateInterval})
		if err != nil {
			return nil, err
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "vad-capabilities-json",
			"events": []map[string]any{
				{
					"name":               "capabilities_json",
					"update_interval":    fields["update_interval"],
					"has_go_field_names": hasAnyKey(fields, "UpdateInterval"),
				},
			},
		}, nil
	case "capabilities_required_update_interval":
		var missing lkvad.VADCapabilities
		err := json.Unmarshal([]byte(`{}`), &missing)
		missingField := ""
		if err != nil && strings.Contains(err.Error(), "update_interval") {
			missingField = "update_interval"
		}
		var zero lkvad.VADCapabilities
		if err := json.Unmarshal([]byte(`{"update_interval":0}`), &zero); err != nil {
			return nil, err
		}
		return map[string]any{
			"contract": "vad-capabilities-required-field",
			"events": []map[string]any{
				{
					"name":                 "capabilities_required_update_interval",
					"missing_required":     missingField == "update_interval",
					"missing_field":        missingField,
					"zero_update_interval": zero.UpdateInterval,
				},
			},
		}, nil
	case "event_json":
		data, err := json.Marshal(lkvad.VADEvent{
			Type:                  lkvad.VADEventInferenceDone,
			SamplesIndex:          320,
			Timestamp:             1.25,
			SpeechDuration:        0.5,
			SilenceDuration:       0.75,
			Probability:           0.875,
			InferenceDuration:     0.01,
			Speaking:              true,
			RawAccumulatedSilence: 0.125,
			RawAccumulatedSpeech:  0.25,
		})
		if err != nil {
			return nil, err
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		frames, _ := fields["frames"].([]any)
		return map[string]any{
			"contract": "vad-event-json",
			"events": []map[string]any{
				{
					"name":                    "event_json",
					"type":                    fields["type"],
					"samples_index":           fields["samples_index"],
					"timestamp":               fields["timestamp"],
					"speech_duration":         fields["speech_duration"],
					"silence_duration":        fields["silence_duration"],
					"frames_length":           len(frames),
					"probability":             fields["probability"],
					"inference_duration":      fields["inference_duration"],
					"speaking":                fields["speaking"],
					"raw_accumulated_silence": fields["raw_accumulated_silence"],
					"raw_accumulated_speech":  fields["raw_accumulated_speech"],
					"has_go_field_names": hasAnyKey(
						fields,
						"SamplesIndex",
						"SpeechDuration",
						"InferenceDuration",
					),
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported vad value-object action %q", payload.Action)
	}
}
