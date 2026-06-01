package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestRunContextDisallowInterruptionsUpdatesSpeechHandle(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	if err := runCtx.DisallowInterruptions(); err != nil {
		t.Fatalf("DisallowInterruptions error = %v, want nil", err)
	}

	if speech.AllowInterruptions {
		t.Fatal("speech.AllowInterruptions = true, want false")
	}
	if err := speech.Interrupt(false); !errors.Is(err, ErrSpeechInterruptionsDisabled) {
		t.Fatalf("Interrupt(false) error = %v, want ErrSpeechInterruptionsDisabled", err)
	}
}

func TestRunContextDisallowInterruptionsFailsAfterInterruption(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v, want nil", err)
	}
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	err := runCtx.DisallowInterruptions()

	if !errors.Is(err, ErrSpeechAlreadyInterrupted) {
		t.Fatalf("DisallowInterruptions error = %v, want ErrSpeechAlreadyInterrupted", err)
	}
}

func TestRunContextWaitForPlayoutWaitsOnlyInitialGenerationStep(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.AuthorizeGeneration()
	runCtx := NewRunContext(nil, speech, &llm.FunctionCall{Name: "lookup"})

	done := make(chan error, 1)
	go func() {
		done <- runCtx.WaitForPlayout(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForPlayout returned before generation finished: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := speech.MarkGenerationDone(); err != nil {
		t.Fatalf("MarkGenerationDone error = %v, want nil", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForPlayout error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForPlayout did not return after initial generation finished")
	}

	if speech.IsDone() {
		t.Fatal("speech is done, want WaitForPlayout to return without requiring full speech completion")
	}
}

func TestFunctionToolsExecutedEventPairsCallsAndOutputs(t *testing.T) {
	callA := &llm.FunctionCall{CallID: "call_a", Name: "lookup"}
	callB := &llm.FunctionCall{CallID: "call_b", Name: "notify"}
	outA := &llm.FunctionCallOutput{CallID: "call_a", Name: "lookup", Output: "ok"}

	ev, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{callA, callB},
		[]*llm.FunctionCallOutput{outA, nil},
	)

	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}
	if ev.GetType() != "function_tools_executed" {
		t.Fatalf("GetType() = %q, want function_tools_executed", ev.GetType())
	}
	if ev.CreatedAt.IsZero() {
		t.Fatal("CreatedAt is zero")
	}
	pairs := ev.Zipped()
	if len(pairs) != 2 {
		t.Fatalf("Zipped length = %d, want 2", len(pairs))
	}
	if pairs[0].FunctionCall != callA || pairs[0].FunctionCallOutput != outA {
		t.Fatalf("first pair = %#v, want callA/outA", pairs[0])
	}
	if pairs[1].FunctionCall != callB || pairs[1].FunctionCallOutput != nil {
		t.Fatalf("second pair = %#v, want callB/nil", pairs[1])
	}
}

func TestFunctionToolsExecutedEventRequiresParallelLists(t *testing.T) {
	_, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{{CallID: "call_a", Name: "lookup"}},
		nil,
	)

	if !errors.Is(err, ErrFunctionToolEventLengthMismatch) {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want ErrFunctionToolEventLengthMismatch", err)
	}
}

func TestFunctionToolsExecutedEventReplyAndHandoffFlagsCanBeCanceled(t *testing.T) {
	ev, err := NewFunctionToolsExecutedEvent(
		[]*llm.FunctionCall{{CallID: "call_a", Name: "lookup"}},
		[]*llm.FunctionCallOutput{{CallID: "call_a", Name: "lookup", Output: "ok"}},
	)
	if err != nil {
		t.Fatalf("NewFunctionToolsExecutedEvent error = %v, want nil", err)
	}
	ev.ReplyRequired = true
	ev.HandoffRequired = true

	ev.CancelToolReply()
	ev.CancelAgentHandoff()

	if ev.HasToolReply() {
		t.Fatal("HasToolReply() = true, want false after CancelToolReply")
	}
	if ev.HasAgentHandoff() {
		t.Fatal("HasAgentHandoff() = true, want false after CancelAgentHandoff")
	}
}

func TestAgentFalseInterruptionEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	ev := NewAgentFalseInterruptionEvent(true)

	var event Event = ev
	if event.GetType() != "agent_false_interruption" {
		t.Fatalf("GetType() = %q, want agent_false_interruption", event.GetType())
	}
	if !ev.Resumed {
		t.Fatal("Resumed = false, want true")
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}

func TestAgentFalseInterruptionEventPreservesDeprecatedCompatibilityFields(t *testing.T) {
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant}
	instructions := "continue from the apology"

	ev := &AgentFalseInterruptionEvent{
		Resumed:           false,
		Message:           msg,
		ExtraInstructions: instructions,
		CreatedAt:         time.Now(),
	}

	if ev.Message != msg {
		t.Fatalf("Message = %#v, want original message", ev.Message)
	}
	if ev.ExtraInstructions != instructions {
		t.Fatalf("ExtraInstructions = %q, want %q", ev.ExtraInstructions, instructions)
	}
}

func TestUserTurnExceededEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	ev := NewUserTurnExceededEvent("latest words", "all accumulated words", 3, 4*time.Second)

	var event Event = ev
	if event.GetType() != "user_turn_exceeded" {
		t.Fatalf("GetType() = %q, want user_turn_exceeded", event.GetType())
	}
	if ev.Transcript != "latest words" {
		t.Fatalf("Transcript = %q, want latest words", ev.Transcript)
	}
	if ev.AccumulatedTranscript != "all accumulated words" {
		t.Fatalf("AccumulatedTranscript = %q, want all accumulated words", ev.AccumulatedTranscript)
	}
	if ev.AccumulatedWordCount != 3 {
		t.Fatalf("AccumulatedWordCount = %d, want 3", ev.AccumulatedWordCount)
	}
	if ev.Duration != 4*time.Second {
		t.Fatalf("Duration = %v, want 4s", ev.Duration)
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}

func TestCloseReasonIncludesTaskCompleted(t *testing.T) {
	ev := &CloseEvent{Reason: CloseReasonTaskCompleted, CreatedAt: time.Now()}

	var event Event = ev
	if event.GetType() != "close" {
		t.Fatalf("GetType() = %q, want close", event.GetType())
	}
	if ev.Reason != "task_completed" {
		t.Fatalf("Reason = %q, want task_completed", ev.Reason)
	}
}

func TestErrorEventIsTypedAndTimestamped(t *testing.T) {
	before := time.Now()
	source := "llm"
	cause := errors.New("provider failed")

	ev := NewErrorEvent(cause, source)

	var event Event = ev
	if event.GetType() != "error" {
		t.Fatalf("GetType() = %q, want error", event.GetType())
	}
	if !errors.Is(ev.Error, cause) {
		t.Fatalf("Error = %v, want provider failure", ev.Error)
	}
	if ev.Source != source {
		t.Fatalf("Source = %#v, want %q", ev.Source, source)
	}
	if ev.CreatedAt.IsZero() || ev.CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want timestamp after %v", ev.CreatedAt, before)
	}
}
