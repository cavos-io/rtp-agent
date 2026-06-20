package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	lkbeta "github.com/cavos-io/rtp-agent/core/beta"
	lkbetatools "github.com/cavos-io/rtp-agent/core/beta/tools"
)

func runDTMFEventCode(input json.RawMessage) (any, error) {
	var payload struct {
		Events []string `json:"events"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	events := payload.Events
	if events == nil {
		events = []string{"a", "12"}
	}
	resultEvents := make([]map[string]any, 0, len(events))
	for _, event := range events {
		code, err := lkbeta.DtmfEventToCode(lkbeta.DtmfEvent(event))
		resultEvents = append(resultEvents, map[string]any{
			"name":        "dtmf_event_to_code",
			"input":       event,
			"code":        code,
			"error":       err != nil,
			"error_class": errorClass(err),
		})
	}
	return map[string]any{"contract": "dtmf-event-code", "events": resultEvents}, nil
}

func runDTMFTool(input json.RawMessage) (any, error) {
	var payload struct {
		Action string   `json:"action"`
		Events []string `json:"events"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "parameters"
	}
	tool := lkbetatools.NewSendDTMFTool(fakeDTMFPublisher{})
	switch payload.Action {
	case "parameters":
		return map[string]any{
			"contract": "send-dtmf-tool",
			"events": []map[string]any{
				{
					"name":       "parameters",
					"parameters": tool.Parameters(),
				},
			},
		}, nil
	case "execute":
		events := payload.Events
		if events == nil {
			events = []string{"X"}
		}
		args, err := json.Marshal(map[string]any{"events": events})
		if err != nil {
			return nil, err
		}
		output, err := tool.Execute(context.Background(), string(args))
		failed := strings.Contains(output, "Failed to send DTMF event:")
		event := map[string]any{
			"name":                   "execute",
			"invalid_event":          firstString(events),
			"output_contains_failed": failed,
			"error":                  err != nil,
			"error_class":            errorClass(err),
		}
		if !failed {
			event["output"] = output
		}
		return map[string]any{
			"contract": "send-dtmf-tool",
			"events":   []map[string]any{event},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported dtmf tool action %q", payload.Action)
	}
}

type fakeDTMFPublisher struct{}

func (fakeDTMFPublisher) PublishDTMF(int32, string) error {
	return nil
}

func runEndCallTool(input json.RawMessage) (any, error) {
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &payload); err != nil {
		return nil, err
	}
	if payload.Action == "" {
		payload.Action = "description"
	}
	tool := lkbetatools.NewEndCallTool(nil, lkbetatools.EndCallToolOptions{})
	switch payload.Action {
	case "description":
		description := tool.Description()
		return map[string]any{
			"contract": "end-call-tool",
			"events": []map[string]any{
				{
					"name":                                 "description",
					"contains_user_done_guidance":          strings.Contains(description, "The user clearly indicates they are done"),
					"contains_agent_completion_trigger":    strings.Contains(description, "agent determines the conversation is complete"),
					"contains_no_pause_hold_transfer_rule": strings.Contains(description, "pause, hold, or transfer"),
				},
			},
		}, nil
	case "parameters":
		return map[string]any{
			"contract": "end-call-tool",
			"events": []map[string]any{
				{
					"name":       "parameters",
					"parameters": tool.Parameters(),
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported end call tool action %q", payload.Action)
	}
}
