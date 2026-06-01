package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestMultimodalToolExecutionMasksInternalErrors(t *testing.T) {
	chatCtx := llm.NewChatContext()
	rtSession := &fakeRealtimeSession{}
	ma := &MultimodalAgent{
		session:   &AgentSession{Tools: []llm.Tool{&fakeGenerationTool{name: "lookup", err: errors.New("database password leaked")}}},
		chatCtx:   chatCtx,
		rtSession: rtSession,
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if !output.IsError || output.Output != "An internal error occurred" {
		t.Fatalf("function output = %#v, want masked internal error", output)
	}
	if rtSession.updated != chatCtx {
		t.Fatalf("updated chat context = %#v, want current context", rtSession.updated)
	}
}

func TestMultimodalToolExecutionSuppressesStopResponse(t *testing.T) {
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		session:   &AgentSession{Tools: []llm.Tool{&fakeGenerationTool{name: "lookup", err: llm.StopResponse{}}}},
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "lookup", CallID: "call_lookup", Arguments: `{}`},
	})

	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat items = %#v, want no output for StopResponse", chatCtx.Items)
	}
}

func TestMultimodalToolExecutionReportsUnknownFunction(t *testing.T) {
	chatCtx := llm.NewChatContext()
	ma := &MultimodalAgent{
		session:   &AgentSession{},
		chatCtx:   chatCtx,
		rtSession: &fakeRealtimeSession{},
		ctx:       context.Background(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:     llm.RealtimeEventTypeFunctionCall,
		Function: &llm.FunctionToolCall{Name: "missing", CallID: "call_missing", Arguments: `{}`},
	})

	output := lastFunctionOutput(t, chatCtx)
	if !output.IsError || output.Output != "Unknown function: missing" {
		t.Fatalf("function output = %#v, want unknown function error", output)
	}
}

func TestMultimodalAgentEmitsErrorEventForRealtimeError(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	model := &fakeRealtimeModel{}
	cause := errors.New("realtime failed")
	ma := &MultimodalAgent{
		model:   model,
		session: session,
		chatCtx: llm.NewChatContext(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: cause,
	})

	select {
	case ev := <-session.ErrorEvents():
		if !errors.Is(ev.Error, cause) {
			t.Fatalf("Error = %v, want %v", ev.Error, cause)
		}
		if ev.Source != model {
			t.Fatalf("Source = %#v, want realtime model", ev.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("ErrorEvents did not receive realtime error")
	}
}

func TestMultimodalAgentDoesNotEmitErrorEventForRealtimeEOF(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	model := &fakeRealtimeModel{}
	ma := &MultimodalAgent{
		model:   model,
		session: session,
		chatCtx: llm.NewChatContext(),
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:  llm.RealtimeEventTypeError,
		Error: io.EOF,
	})

	select {
	case ev := <-session.ErrorEvents():
		t.Fatalf("unexpected realtime EOF error event: %#v", ev)
	default:
	}
}

func lastFunctionOutput(t *testing.T, chatCtx *llm.ChatContext) *llm.FunctionCallOutput {
	t.Helper()
	if len(chatCtx.Items) == 0 {
		t.Fatal("chat context has no items")
	}
	output, ok := chatCtx.Items[len(chatCtx.Items)-1].(*llm.FunctionCallOutput)
	if !ok {
		t.Fatalf("last item = %T, want FunctionCallOutput", chatCtx.Items[len(chatCtx.Items)-1])
	}
	return output
}

type fakeRealtimeModel struct{}

func (f *fakeRealtimeModel) Capabilities() llm.RealtimeCapabilities {
	return llm.RealtimeCapabilities{}
}

func (f *fakeRealtimeModel) Session() (llm.RealtimeSession, error) {
	return &fakeRealtimeSession{}, nil
}

func (f *fakeRealtimeModel) Close() error { return nil }

type fakeRealtimeSession struct {
	updated *llm.ChatContext
}

func (f *fakeRealtimeSession) UpdateInstructions(string) error { return nil }

func (f *fakeRealtimeSession) UpdateChatContext(chatCtx *llm.ChatContext) error {
	f.updated = chatCtx
	return nil
}

func (f *fakeRealtimeSession) UpdateTools([]llm.Tool) error { return nil }

func (f *fakeRealtimeSession) UpdateOptions(llm.RealtimeSessionOptions) error { return nil }

func (f *fakeRealtimeSession) GenerateReply(llm.RealtimeGenerateReplyOptions) error { return nil }

func (f *fakeRealtimeSession) Truncate(llm.RealtimeTruncateOptions) error { return nil }

func (f *fakeRealtimeSession) Interrupt() error { return nil }

func (f *fakeRealtimeSession) Close() error { return nil }

func (f *fakeRealtimeSession) EventCh() <-chan llm.RealtimeEvent {
	ch := make(chan llm.RealtimeEvent)
	close(ch)
	return ch
}

func (f *fakeRealtimeSession) PushAudio(*model.AudioFrame) error { return nil }

func (f *fakeRealtimeSession) PushVideo(*images.VideoFrame) error { return nil }

func (f *fakeRealtimeSession) CommitAudio() error { return nil }

func (f *fakeRealtimeSession) ClearAudio() error { return nil }
