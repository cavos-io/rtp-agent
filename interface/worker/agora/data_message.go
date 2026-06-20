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
	StreamID  string
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
	if strings.TrimSpace(string(msg.Payload)) == "" {
		return nil
	}
	var payload struct {
		DataType string `json:"data_type"`
		Text     string `json:"text"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return err
	}
	if payload.DataType != "input_text" {
		return nil
	}
	return r.TextInput(normalizeContext(ctx), TextInputEvent{
		Text:      payload.Text,
		StreamID:  defaultRTMStreamID(""),
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

type TextInputTranscriber interface {
	EmitUserInputTranscribed(agent.UserInputTranscribedEvent)
}

func HandleTextInput(ctx context.Context, responder TextResponder, text string) error {
	return HandleTextInputEvent(ctx, responder, TextInputEvent{Text: text})
}

func HandleTextInputEvent(ctx context.Context, responder TextResponder, ev TextInputEvent) error {
	if responder == nil {
		return nil
	}
	ctx = normalizeContext(ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if ev.Text == "" {
		return nil
	}
	run := func(ctx context.Context) error {
		if err := responder.Interrupt(false); err != nil {
			return err
		}
		_, err := responder.GenerateReply(ctx, ev.Text)
		if err != nil {
			return err
		}
		if transcriber, ok := responder.(TextInputTranscriber); ok {
			transcriber.EmitUserInputTranscribed(agent.UserInputTranscribedEvent{
				Transcript: ev.Text,
				IsFinal:    true,
				SpeakerID:  defaultRTMStreamID(ev.StreamID),
			})
		}
		return nil
	}
	if claimer, ok := responder.(TextTurnClaimer); ok {
		return claimer.ClaimUserTurn(ctx, run)
	}
	return run(ctx)
}

func defaultRTMStreamID(streamID string) string {
	streamID = strings.TrimSpace(streamID)
	if streamID == "" {
		return "0"
	}
	return streamID
}
