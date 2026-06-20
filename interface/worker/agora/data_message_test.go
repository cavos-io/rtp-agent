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
	if got.StreamID != "0" {
		t.Fatalf("StreamID = %q, want TEN default stream id 0", got.StreamID)
	}
}

func TestRTMMessageRouterDropsForeignChannel(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		Channel: "support",
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Channel:   "sales",
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"input_text","text":"wrong room"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil for foreign channel", err)
	}
	if called {
		t.Fatal("foreign-channel RTM input_text message was dispatched")
	}
}

func TestRTMMessageRouterDispatchesEmptyInputText(t *testing.T) {
	called := false
	var got TextInputEvent
	router := RTMMessageRouter{
		TextInput: func(_ context.Context, ev TextInputEvent) error {
			called = true
			got = ev
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Channel:   "support",
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"input_text"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if !called {
		t.Fatal("empty TEN input_text message was not dispatched")
	}
	if got.Text != "" || got.Publisher != "caller-7" || got.Channel != "support" {
		t.Fatalf("TextInputEvent = %#v, want empty TEN input_text event", got)
	}
}

func TestRTMMessageRouterDispatchesTypeOnlyInputText(t *testing.T) {
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
		Payload:   []byte(`{"type":"input_text","text":"hello from ten","stream_id":"caller-7"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if got.Text != "hello from ten" || got.Publisher != "caller-7" || got.Channel != "support" {
		t.Fatalf("TextInputEvent = %#v, want TEN frontend type input_text event", got)
	}
	if got.StreamID != "0" {
		t.Fatalf("StreamID = %q, want TEN default stream id 0", got.StreamID)
	}
}

func TestRTMMessageRouterDefaultsMissingStreamID(t *testing.T) {
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
		Payload:   []byte(`{"data_type":"input_text","text":"hello from ten"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if got.StreamID != "0" {
		t.Fatalf("StreamID = %q, want TEN default stream id 0", got.StreamID)
	}
}

func TestRTMMessageRouterIgnoresPayloadStreamID(t *testing.T) {
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
		Payload:   []byte(`{"data_type":"input_text","text":"hello from ten","stream_id":100}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if got.StreamID != "0" {
		t.Fatalf("StreamID = %q, want TEN backend default stream id 0", got.StreamID)
	}
	if got.Text != "hello from ten" {
		t.Fatalf("Text = %q, want payload text still dispatched", got.Text)
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

func TestRTMMessageRouterIgnoresTrimmedSelfPublisher(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		AgentUserID: " agent-rtm ",
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Publisher: " agent-rtm ",
		Payload:   []byte(`{"data_type":"input_text","text":"loop"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if called {
		t.Fatal("trimmed self-published RTM message was dispatched")
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

func TestRTMMessageRouterDoesNotFallbackWhenDataTypePresent(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}

	err := router.HandleDataMessage(context.Background(), DataMessage{
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"transcribe","type":"input_text","text":"agent transcript"}`),
	})
	if err != nil {
		t.Fatalf("HandleDataMessage() error = %v, want nil", err)
	}
	if called {
		t.Fatal("type fallback overrode data_type discriminator")
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

func TestRTMMessageRouterStopsOnCanceledContextBeforeDispatch(t *testing.T) {
	called := false
	router := RTMMessageRouter{
		TextInput: func(context.Context, TextInputEvent) error {
			called = true
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := router.HandleDataMessage(ctx, DataMessage{
		Channel:   "support",
		Publisher: "caller-7",
		Payload:   []byte(`{"data_type":"input_text","text":"late turn"}`),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleDataMessage() error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("canceled RTM message was dispatched")
	}
}

func TestHandleTextInputInterruptsBeforeGenerateReply(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInput(context.Background(), responder, "hello")
	if err != nil {
		t.Fatalf("HandleTextInput() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) < 2 || got[0] != "interrupt:false" || got[1] != "generate:hello" {
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

func TestHandleTextInputEventEmitsTranscriptAfterGenerateReply(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInputEvent(context.Background(), responder, TextInputEvent{
		Text:     "hello",
		StreamID: "caller-7",
	})
	if err != nil {
		t.Fatalf("HandleTextInputEvent() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) != 3 || got[0] != "interrupt:false" || got[1] != "generate:hello" || got[2] != "transcript:hello:caller-7" {
		t.Fatalf("calls = %#v, want interrupt, generate, transcript", got)
	}
}

func TestHandleTextInputDefaultsTranscriptStreamID(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInput(context.Background(), responder, "hello")
	if err != nil {
		t.Fatalf("HandleTextInput() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) != 3 || got[2] != "transcript:hello:0" {
		t.Fatalf("calls = %#v, want default TEN stream id 0 on user transcript", got)
	}
}

func TestHandleTextInputEventIgnoresEmptyText(t *testing.T) {
	responder := &recordingTextResponder{}

	err := HandleTextInputEvent(context.Background(), responder, TextInputEvent{
		Text:     "",
		StreamID: "caller-7",
	})
	if err != nil {
		t.Fatalf("HandleTextInputEvent() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) != 0 {
		t.Fatalf("calls = %#v, want empty text ignored before interrupt/generate/transcript", got)
	}
	if responder.claimed {
		t.Fatal("HandleTextInputEvent() claimed user turn for empty input_text")
	}
	if err := HandleTextInput(context.Background(), responder, ""); err != nil {
		t.Fatalf("HandleTextInput() error = %v, want nil", err)
	}
	if got := responder.calls; len(got) != 0 {
		t.Fatalf("calls after direct empty input = %#v, want still ignored", got)
	}
}

func TestHandleTextInputEventStopsOnCanceledContextBeforeSideEffects(t *testing.T) {
	responder := &recordingTextResponder{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := HandleTextInputEvent(ctx, responder, TextInputEvent{Text: "hello"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleTextInputEvent() error = %v, want context canceled", err)
	}
	if got := responder.calls; len(got) != 0 {
		t.Fatalf("calls = %#v, want canceled text turn to skip interrupt/generate/transcript", got)
	}
	if responder.claimed {
		t.Fatal("HandleTextInputEvent() claimed user turn after context canceled")
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

func (r *recordingTextResponder) EmitUserInputTranscribed(ev agent.UserInputTranscribedEvent) {
	r.calls = append(r.calls, "transcript:"+ev.Transcript+":"+ev.SpeakerID)
}
