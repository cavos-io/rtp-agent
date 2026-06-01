package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
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

func TestMultimodalAgentEmitsFinalInputTranscriptionAndCommitsUserMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := &MultimodalAgent{
		session: session,
		chatCtx: chatCtx,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_user_1",
			Transcript: "hello realtime",
			IsFinal:    true,
		},
	})

	select {
	case ev := <-session.UserInputTranscribedEvents():
		if ev.Transcript != "hello realtime" || !ev.IsFinal {
			t.Fatalf("transcription event = %#v, want final hello realtime", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive realtime transcript")
	}

	select {
	case ev := <-session.ConversationItemAddedEvents():
		msg, ok := ev.Item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("event item = %T, want *llm.ChatMessage", ev.Item)
		}
		if msg.ID != "item_user_1" || msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello realtime" {
			t.Fatalf("message = %#v, want committed user message with realtime transcript", msg)
		}
		if chatCtx.GetByID("item_user_1") != msg {
			t.Fatalf("chat context item = %#v, want committed message", chatCtx.GetByID("item_user_1"))
		}
	case <-time.After(time.Second):
		t.Fatal("ConversationItemAddedEvents did not receive realtime user message")
	}
}

func TestMultimodalAgentEmitsInterimInputTranscriptionWithoutCommittingMessage(t *testing.T) {
	chatCtx := llm.NewChatContext()
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	ma := &MultimodalAgent{
		session: session,
		chatCtx: chatCtx,
	}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeInputAudioTranscriptionCompleted,
		InputTranscription: &llm.InputTranscriptionCompleted{
			ItemID:     "item_user_1",
			Transcript: "hello",
			IsFinal:    false,
		},
	})

	select {
	case ev := <-session.UserInputTranscribedEvents():
		if ev.Transcript != "hello" || ev.IsFinal {
			t.Fatalf("transcription event = %#v, want interim hello", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("UserInputTranscribedEvents did not receive interim realtime transcript")
	}
	select {
	case ev := <-session.ConversationItemAddedEvents():
		t.Fatalf("unexpected conversation item for interim transcript: %#v", ev)
	default:
	}
	if len(chatCtx.Items) != 0 {
		t.Fatalf("chat context items = %#v, want no interim transcript message", chatCtx.Items)
	}
}

func TestMultimodalAgentForwardsRealtimeMetrics(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	metrics := &telemetry.RealtimeModelMetrics{RequestID: "req_1"}
	ma := &MultimodalAgent{session: session, chatCtx: llm.NewChatContext()}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type:    llm.RealtimeEventTypeMetricsCollected,
		Metrics: metrics,
	})

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("Metrics = %#v, want original realtime metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive realtime metrics")
	}
}

func TestMultimodalAgentAddsServerRemoteItemPlaceholder(t *testing.T) {
	existing := &llm.ChatMessage{
		ID:        "item_user_1",
		Role:      llm.ChatRoleUser,
		Content:   []llm.ChatContent{{Text: "hello"}},
		CreatedAt: time.Now(),
	}
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: existing.CreatedAt.Add(time.Second),
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(existing)
	ma := &MultimodalAgent{chatCtx: chatCtx}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			PreviousItemID: "item_user_1",
			Item:           remote,
		},
	})

	if len(chatCtx.Items) != 2 || chatCtx.Items[1] != remote {
		t.Fatalf("chat context items = %#v, want remote item appended after previous item", chatCtx.Items)
	}
}

func TestMultimodalAgentSkipsDuplicateRemoteItemPlaceholder(t *testing.T) {
	remote := &llm.ChatMessage{
		ID:        "item_assistant_1",
		Role:      llm.ChatRoleAssistant,
		Content:   []llm.ChatContent{{Text: "hi"}},
		CreatedAt: time.Now(),
	}
	chatCtx := llm.NewChatContext()
	chatCtx.Insert(remote)
	ma := &MultimodalAgent{chatCtx: chatCtx}

	ma.handleRealtimeEvent(llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeRemoteItemAdded,
		RemoteItem: &llm.RemoteItemAddedEvent{
			Item: remote,
		},
	})

	if len(chatCtx.Items) != 1 {
		t.Fatalf("chat context items = %#v, want duplicate remote item skipped", chatCtx.Items)
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
