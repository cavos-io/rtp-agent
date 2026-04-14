package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/cavos-io/conversation-worker/core/beta"
)

type DtmfPublisher interface {
	PublishDTMF(code int32, digit string) error
}

type SendDTMFTool struct {
	publisher DtmfPublisher
}

func NewSendDTMFTool(publisher DtmfPublisher) *SendDTMFTool {
	return &SendDTMFTool{
		publisher: publisher,
	}
}

func (t *SendDTMFTool) ID() string {
	return "send_dtmf_events"
}

func (t *SendDTMFTool) Name() string {
	return "send_dtmf_events"
}

func (t *SendDTMFTool) Description() string {
	return `Send a list of DTMF events to the telephony provider.

Call when:
- User wants to send DTMF events`
}

func (t *SendDTMFTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"events": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0", "*", "#", "A", "B", "C", "D"},
				},
			},
		},
		"required": []string{"events"},
	}
}

type sendDTMFArgs struct {
	Events []beta.DtmfEvent `json:"events"`
}

func (t *SendDTMFTool) Args() any {
	return &sendDTMFArgs{}
}

func (t *SendDTMFTool) Execute(ctx context.Context, args any) (any, error) {
	var a *sendDTMFArgs
	if typed, ok := args.(*sendDTMFArgs); ok {
		a = typed
	} else {
		// Fallback to manual binding if needed (though PerformToolExecutions handles it)
		m, _ := args.(map[string]any)
		eventsRaw, _ := m["events"].([]interface{})
		a = &sendDTMFArgs{}
		for _, raw := range eventsRaw {
			if evStr, ok := raw.(string); ok {
				a.Events = append(a.Events, beta.DtmfEvent(evStr))
			}
		}
	}

	if t.publisher == nil {
		return "", fmt.Errorf("DTMF publisher not available")
	}

	for _, event := range a.Events {
		code, err := beta.DtmfEventToCode(event)
		if err != nil {
			return "", err
		}

		err = t.publisher.PublishDTMF(int32(code), string(event))
		if err != nil {
			return fmt.Sprintf("Failed to send DTMF event: %s. Error: %v", event, err), nil
		}

		// Wait for publish delay (0.3s)
		select {
		case <-ctx.Done():
			return "Cancelled", ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	return fmt.Sprintf("Successfully sent DTMF events: %s", beta.FormatDtmf(a.Events)), nil
}
