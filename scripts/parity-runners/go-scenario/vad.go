package main

import (
	"encoding/json"
	"fmt"

	lkvad "github.com/cavos-io/rtp-agent/core/vad"
)

func runVADValueObjects(input json.RawMessage) (any, error) {
	var payload struct {
		Action         string  `json:"action"`
		UpdateInterval float64 `json:"update_interval"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "capabilities_json"
	}
	if payload.UpdateInterval == 0 {
		payload.UpdateInterval = 0.5
	}

	switch payload.Action {
	case "capabilities_json":
		data, err := json.Marshal(lkvad.VADCapabilities{UpdateInterval: payload.UpdateInterval})
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
	default:
		return nil, fmt.Errorf("unsupported vad value-object action %q", payload.Action)
	}
}
