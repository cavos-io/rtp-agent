package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestAgentSessionGenerateReplyReturnsScheduledSpeechHandle(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	handle, err := session.GenerateReply(context.Background(), "hello")

	if err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}
	if handle == nil {
		t.Fatal("GenerateReply handle = nil, want speech handle")
	}
	if !handle.IsScheduled() {
		t.Fatal("GenerateReply returned unscheduled handle")
	}
	if !handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = false, want session default true")
	}
	if got, want := handle.InputDetails.Modality, "text"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionGenerateReplyAddsUserInputToChatContext(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	if _, err := session.GenerateReply(context.Background(), "hello"); err != nil {
		t.Fatalf("GenerateReply error = %v, want nil", err)
	}

	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("ChatCtx.Items length = %d, want 1", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("ChatCtx item type = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleUser || msg.TextContent() != "hello" {
		t.Fatalf("ChatCtx message = %#v, want user message with text hello", msg)
	}
}

func TestAgentSessionGenerateReplyOptionsOverrideInterruptionsAndInputModality(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)
	allowInterruptions := false

	handle, err := session.GenerateReplyWithOptions(context.Background(), GenerateReplyOptions{
		UserInput:          "hello",
		AllowInterruptions: &allowInterruptions,
		InputModality:      "audio",
	})

	if err != nil {
		t.Fatalf("GenerateReplyWithOptions error = %v, want nil", err)
	}
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want per-call false override")
	}
	if got, want := handle.InputDetails.Modality, "audio"; got != want {
		t.Fatalf("handle.InputDetails.Modality = %q, want %q", got, want)
	}
}

func TestAgentSessionRunReturnsRunResultWatchingGeneratedSpeech(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{AllowInterruptions: true})
	session.activity = NewAgentActivity(agent, session)

	result, err := session.Run(context.Background(), "hello")

	if err != nil {
		t.Fatalf("Run error = %v, want nil", err)
	}
	if result == nil {
		t.Fatal("Run result = nil, want RunResult")
	}
	if got := result.UserInput(); got != "hello" {
		t.Fatalf("UserInput = %q, want hello", got)
	}

	handle := session.activity.speechQueue[0].speech
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant, CreatedAt: time.Now()}
	handle.AddChatItems(msg)
	handle.MarkDone()

	if !result.Done() {
		t.Fatal("Run result not done after generated speech completed")
	}
	events := result.Events()
	if len(events) != 1 {
		t.Fatalf("Events length = %d, want 1", len(events))
	}
	if ev, ok := events[0].(*ChatMessageEvent); !ok || ev.Item != msg {
		t.Fatalf("events[0] = %#v, want recorded assistant message", events[0])
	}
}

func TestAgentSessionRunRejectsNestedActiveRun(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)

	first, err := session.Run(context.Background(), "first")
	if err != nil {
		t.Fatalf("first Run error = %v, want nil", err)
	}

	second, err := session.Run(context.Background(), "second")

	if second != nil {
		t.Fatalf("second Run result = %#v, want nil", second)
	}
	if !errors.Is(err, ErrAgentSessionNestedRun) {
		t.Fatalf("second Run error = %v, want ErrAgentSessionNestedRun", err)
	}

	session.activity.speechQueue[0].speech.MarkDone()
	if !first.Done() {
		t.Fatal("first Run result not done after scheduled speech completed")
	}
}

func TestAgentSessionGenerateReplyRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	handle, err := session.GenerateReply(context.Background(), "hello")

	if handle != nil {
		t.Fatalf("GenerateReply handle = %#v, want nil when session is not running", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionCloseSoonStopsRunningSession(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	session.started = true

	session.CloseSoon(CloseReasonParticipantDisconnected)

	select {
	case ev := <-session.CloseEvents():
		if ev.Reason != CloseReasonParticipantDisconnected {
			t.Fatalf("CloseEvent.Reason = %q, want participant_disconnected", ev.Reason)
		}
	default:
		t.Fatal("CloseSoon did not emit close event")
	}

	handle, err := session.GenerateReply(context.Background(), "hello")
	if handle != nil {
		t.Fatalf("GenerateReply handle after CloseSoon = %#v, want nil", handle)
	}
	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("GenerateReply error after CloseSoon = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionInterruptRequiresRunningActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})

	err := session.Interrupt(false)

	if !errors.Is(err, ErrAgentSessionNotRunning) {
		t.Fatalf("Interrupt error = %v, want ErrAgentSessionNotRunning", err)
	}
}

func TestAgentSessionInterruptDelegatesToActivity(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	current := NewSpeechHandle(true, DefaultInputDetails())
	session.activity.currentSpeech = current

	done := make(chan error, 1)
	go func() {
		done <- session.Interrupt(false)
	}()

	waitForInterrupted(t, current)
	current.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Interrupt error = %v, want nil", err)
		}
	case <-testTimeout():
		t.Fatal("Interrupt did not return after current speech was done")
	}
}

func testTimeout() <-chan time.Time {
	return time.After(time.Second)
}

func TestAgentSessionUpdateAgentStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateAgentState(AgentStateThinking)

	select {
	case ev := <-session.AgentStateChangedCh:
		var event Event = &ev
		if event.GetType() != "agent_state_changed" {
			t.Fatalf("event type = %q, want agent_state_changed", event.GetType())
		}
		if ev.OldState != "" || ev.NewState != AgentStateThinking {
			t.Fatalf("event states = %q -> %q, want empty -> thinking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateAgentState did not emit an event")
	}
}

func TestAgentSessionUpdateUserStateEmitsTypedTimestampedEvent(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	before := time.Now()

	session.UpdateUserState(UserStateSpeaking)

	select {
	case ev := <-session.UserStateChangedCh:
		var event Event = &ev
		if event.GetType() != "user_state_changed" {
			t.Fatalf("event type = %q, want user_state_changed", event.GetType())
		}
		if ev.OldState != "" || ev.NewState != UserStateSpeaking {
			t.Fatalf("event states = %q -> %q, want empty -> speaking", ev.OldState, ev.NewState)
		}
		if ev.CreatedAt.Before(before) || ev.CreatedAt.IsZero() {
			t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateUserState did not emit an event")
	}
}
