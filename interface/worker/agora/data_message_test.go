package agora

import (
	"context"
	"errors"
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestRTMMessageRouterDispatchesInputText(t *testing.T) {
	var got TextInputEvent
	router := RTMMessageRouter{
		TextInput: func(_ context.Context, ev TextInputEvent) error {
			got = ev
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Channel:   "support",
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"input_text","text":"hello from chat"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if got.Text != "hello from chat" || got.Publisher != "caller-7" || got.Channel != "support" {
		t.Fatalf("TextInputEvent = %#v, want parsed input_text event", got)
	}
}

func TestRTMMessageRouterIgnoresSelfPublisher(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		AgentUserID: "agent-rtm",
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Publisher: "agent-rtm",
		Payload:   []byte(`{"data_type":"input_text","text":"loop"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if called {
		t.Fatal("self-published RTM message was dispatched")
	}
}

func TestRTMMessageRouterIgnoresNonInputText(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"transcribe","text":"agent transcript"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if called {
		t.Fatal("non-input_text RTM message was dispatched")
	}
}

func TestRTMMessageRouterIgnoresEmptyPayload(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Publisher: "caller-7",
		Payload:   nil,
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if called {
		t.Fatal("empty RTM message was dispatched")
	}
}

func TestHandleTextInputInterruptsBeforeGenerateReply(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInput(context.Background(), responder, "hello")
	if err != nil {
		t.Fatalf("HandleTextInput() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) != 2 || got[0] != "interrupt:false" || got[1] != "generate:hello" {
		t.Fatalf("calls = %#v, want interrupt before generate reply", got)
	}
}

func TestHandleTextInputUsesTurnClaimWhenAvailable(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInput(context.Background(), responder, "hello")
	if err != nil {
		t.Fatalf("HandleTextInput() error = %v, want nil", err)
	}
	if !responder.claimed {
		t.Fatal("HandleTextInput() did not claim user turn")
	}
}

type recordingTextResponder struct {
	calls   []string
	claimed bool
	err     error
}

func (r *recordingTextResponder) Interrupt(force bool) error {
	r.calls = append(r.calls, "interrupt:false")
	if force {
		r.calls[len(r.calls)-1] = "interrupt:true"
	}
	return r.err
}

func (r *recordingTextResponder) GenerateReply(_ context.Context, text string) (*agent.SpeechHandle, error) {
	r.calls = append(r.calls, "generate:"+text)
	return nil, r.err
}

func (r *recordingTextResponder) ClaimUserTurn(ctx context.Context, fn func(context.Context) error) error {
	r.claimed = true
	if fn == nil {
		return errors.New("nil claim function")
	}
	return fn(ctx)
}
