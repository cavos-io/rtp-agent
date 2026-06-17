package agora

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cavos-io/rtp-agent/core/agent"
)

type DataMessage struct {
	Channel   string
	Publisher string
	Payload   []byte
}

type DataMessageHandler func(context.Context, DataMessage) error

type DataMessageSubscriber interface {
	SetDataMessageHandler(DataMessageHandler)
}

type TextInputEvent struct {
	Text      string
	Channel   string
	Publisher string
}

type TextInputHandler func(context.Context, TextInputEvent) error

type RTMMessageRouter struct {
	AgentUserID string
	TextInput   TextInputHandler
}

func (r RTMMessageRouter) HandleDataMessage(ctx context.Context, msg DataMessage) error {
	if r.TextInput == nil {
		return nil
	}
	if strings.TrimSpace(r.AgentUserID) != "" && msg.Publisher == strings.TrimSpace(r.AgentUserID) {
		return nil
	}
	var payload struct {
		DataType string `json:"data_type"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return err
	}
	if payload.DataType != "input_text" || payload.Text == "" {
		return nil
	}
	return r.TextInput(normalizeContext(ctx), TextInputEvent{
		Text:      payload.Text,
		Channel:   msg.Channel,
		Publisher: msg.Publisher,
	})
}

type TextResponder interface {
	Interrupt(force bool) error
	GenerateReply(context.Context, string) (*agent.SpeechHandle, error)
}

type TextTurnClaimer interface {
	ClaimUserTurn(context.Context, func(context.Context) error) error
}

func HandleTextInput(ctx context.Context, responder TextResponder, text string) error {
	if responder == nil {
		return nil
	}
	run := func(ctx context.Context) error {
		if err := responder.Interrupt(false); err != nil {
			return err
		}
		_, err := responder.GenerateReply(ctx, text)
		return err
	}
	if claimer, ok := responder.(TextTurnClaimer); ok {
		return claimer.ClaimUserTurn(normalizeContext(ctx), run)
	}
	return run(normalizeContext(ctx))
}
