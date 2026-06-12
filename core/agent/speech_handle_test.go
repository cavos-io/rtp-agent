package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestNewSpeechHandleDefaultsZeroInputDetails(t *testing.T) {
	speech := NewSpeechHandle(true, InputDetails{})

	if speech.InputDetails != DefaultInputDetails() {
		t.Fatalf("InputDetails = %#v, want default %#v", speech.InputDetails, DefaultInputDetails())
	}
}

func TestSpeechHandleInterruptDisallowedReturnsError(t *testing.T) {
	speech := NewSpeechHandle(false, DefaultInputDetails())

	err := speech.Interrupt(false)

	if !errors.Is(err, ErrSpeechInterruptionsDisabled) {
		t.Fatalf("Interrupt(false) error = %v, want ErrSpeechInterruptionsDisabled", err)
	}
	if got, want := err.Error(), "This generation handle does not allow interruptions"; got != want {
		t.Fatalf("Interrupt(false) error message = %q, want reference message %q", got, want)
	}
	if speech.IsInterrupted() {
		t.Fatal("speech was interrupted, want interruption rejected")
	}
}

func TestSpeechHandleForceInterruptBypassesDisallowedInterruptions(t *testing.T) {
	speech := NewSpeechHandle(false, DefaultInputDetails())

	if err := speech.Interrupt(true); err != nil {
		t.Fatalf("Interrupt(true) error = %v, want nil", err)
	}

	if !speech.IsInterrupted() {
		t.Fatal("speech was not interrupted, want force interrupt to bypass guard")
	}
}

func TestSpeechHandleDisallowInterruptionsAfterInterruptFails(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	if err := speech.Interrupt(false); err != nil {
		t.Fatalf("Interrupt(false) error = %v, want nil", err)
	}

	err := speech.SetAllowInterruptions(false)

	if !errors.Is(err, ErrSpeechAlreadyInterrupted) {
		t.Fatalf("SetAllowInterruptions(false) error = %v, want ErrSpeechAlreadyInterrupted", err)
	}
	if got, want := err.Error(), "Cannot set allow_interruptions to False, the SpeechHandle is already interrupted"; got != want {
		t.Fatalf("SetAllowInterruptions(false) error message = %q, want reference message %q", got, want)
	}
	if !speech.AllowInterruptions {
		t.Fatal("AllowInterruptions changed to false after interruption, want unchanged")
	}
}

func TestSpeechHandleRunFinalOutput(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	if _, ok := speech.RunFinalOutput(); ok {
		t.Fatal("RunFinalOutput ok = true, want false before SetRunFinalOutput")
	}

	speech.SetRunFinalOutput("done")

	output, ok := speech.RunFinalOutput()
	if !ok {
		t.Fatal("RunFinalOutput ok = false, want true after SetRunFinalOutput")
	}
	if output != "done" {
		t.Fatalf("RunFinalOutput = %#v, want done", output)
	}
}

func TestSpeechHandleWaitIfNotInterruptedReturnsWhenWorkCompletes(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	workDone := make(chan error, 1)
	workDone <- nil

	if err := speech.WaitIfNotInterrupted(context.Background(), workDone); err != nil {
		t.Fatalf("WaitIfNotInterrupted error = %v, want nil", err)
	}
}

func TestSpeechHandleWaitIfNotInterruptedSuppressesWorkErrors(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	workDone := make(chan error, 1)
	workDone <- errors.New("work failed")

	if err := speech.WaitIfNotInterrupted(context.Background(), workDone); err != nil {
		t.Fatalf("WaitIfNotInterrupted error = %v, want nil for reference return_exceptions behavior", err)
	}
}

func TestSpeechHandleWaitIfNotInterruptedWaitsForAllWork(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	firstDone <- nil

	done := make(chan error, 1)
	go func() {
		done <- speech.WaitIfNotInterrupted(context.Background(), firstDone, secondDone)
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitIfNotInterrupted returned before all work completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	secondDone <- nil

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitIfNotInterrupted error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitIfNotInterrupted did not return after all work completed")
	}
}

func TestSpeechHandleWaitIfNotInterruptedReturnsOnInterrupt(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	workDone := make(chan error)

	go func() {
		time.Sleep(10 * time.Millisecond)
		if err := speech.Interrupt(false); err != nil {
			t.Errorf("Interrupt(false) error = %v, want nil", err)
		}
	}()

	err := speech.WaitIfNotInterrupted(context.Background(), workDone)

	if err != nil {
		t.Fatalf("WaitIfNotInterrupted error = %v, want nil after interrupt", err)
	}
	if !speech.IsInterrupted() {
		t.Fatal("speech was not interrupted, want interrupt to wake WaitIfNotInterrupted")
	}
}

func TestSpeechHandleGenerationIDsTrackSteps(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	if got, want := speech.GenerationID(), speech.ID+"_1"; got != want {
		t.Fatalf("GenerationID() = %q, want %q", got, want)
	}
	if got := speech.ParentGenerationID(); got != "" {
		t.Fatalf("ParentGenerationID() = %q, want empty for first step", got)
	}

	speech.IncrementStep()

	if got, want := speech.GenerationID(), speech.ID+"_2"; got != want {
		t.Fatalf("GenerationID() = %q, want %q", got, want)
	}
	if got, want := speech.ParentGenerationID(), speech.ID+"_1"; got != want {
		t.Fatalf("ParentGenerationID() = %q, want %q", got, want)
	}
}

func TestSpeechHandleDoneCallbackRunsWhenMarkedDone(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	called := 0

	speech.AddDoneCallback(func(doneSpeech *SpeechHandle) {
		if doneSpeech != speech {
			t.Fatalf("done callback speech = %p, want %p", doneSpeech, speech)
		}
		called++
	})

	speech.MarkDone()
	speech.MarkDone()

	if called != 1 {
		t.Fatalf("done callback called %d times, want 1", called)
	}
}

func TestSpeechHandleDoneCallbackPanicDoesNotBlockOtherCallbacks(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	called := false

	speech.AddDoneCallback(func(*SpeechHandle) {
		panic("done callback failed")
	})
	speech.AddDoneCallback(func(*SpeechHandle) {
		called = true
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("MarkDone panic = %v, want callback panic isolated", recovered)
		}
		if !called {
			t.Fatal("second done callback was not called after first callback panic")
		}
	}()

	speech.MarkDone()
}

func TestSpeechHandleDoneCallbackAddedAfterDoneRunsImmediately(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.MarkDone()

	called := false
	speech.AddDoneCallback(func(doneSpeech *SpeechHandle) {
		called = doneSpeech == speech
	})

	if !called {
		t.Fatal("done callback added after MarkDone was not called")
	}
}

func TestSpeechHandleRemoveDoneCallback(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	called := false

	remove := speech.AddDoneCallback(func(*SpeechHandle) {
		called = true
	})
	remove()

	speech.MarkDone()

	if called {
		t.Fatal("removed done callback was called")
	}
}

func TestSpeechHandleWaitRejectsOwningFunctionTool(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	functionCall := &llm.FunctionCall{Name: "lookup", CallID: "call_lookup", Extra: map[string]any{}}
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	ctx := WithRunContext(deadlineCtx, NewRunContext(nil, speech, functionCall))

	err := speech.Wait(ctx)

	if err == nil {
		t.Fatal("Wait error = nil, want circular function-tool wait error")
	}
	if got, want := err.Error(), "cannot call `SpeechHandle.wait_for_playout()` from inside the function tool `lookup` that owns this SpeechHandle. This creates a circular wait: the speech handle is waiting for the function tool to complete, while the function tool is simultaneously waiting for the speech handle.\nTo wait for the assistant's spoken response prior to running this tool, use `RunContext.wait_for_playout()` instead"; got != want {
		t.Fatalf("Wait error message = %q, want reference message %q", got, want)
	}
}

func TestSpeechHandleWaitAllowsNonBlockingFunctionTool(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	functionCall := &llm.FunctionCall{
		Name:   "lookup",
		CallID: "call_lookup",
		Extra:  map[string]any{"__livekit_agents_tool_non_blocking": true},
	}
	ctx := WithRunContext(context.Background(), NewRunContext(nil, speech, functionCall))
	speech.MarkDone()

	if err := speech.Wait(ctx); err != nil {
		t.Fatalf("Wait error = %v, want nil for nonblocking function tool", err)
	}
}

func TestSpeechHandleAddChatItemsStoresItemsAndRunsCallbacks(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant}
	var callbackItems []llm.ChatItem

	speech.AddItemAddedCallback(func(item llm.ChatItem) {
		callbackItems = append(callbackItems, item)
		if got := len(speech.ChatItems()); got != 0 {
			t.Fatalf("callback observed %d stored items, want callback before append", got)
		}
	})

	speech.AddChatItems(msg)

	if len(callbackItems) != 1 || callbackItems[0] != msg {
		t.Fatalf("callbackItems = %#v, want callback with added message", callbackItems)
	}
	items := speech.ChatItems()
	if len(items) != 1 || items[0] != msg {
		t.Fatalf("ChatItems() = %#v, want stored message", items)
	}

	items[0] = nil
	if got := speech.ChatItems()[0]; got != msg {
		t.Fatalf("ChatItems returned mutable backing storage, got %#v want original message", got)
	}
}

func TestSpeechHandleItemCallbackPanicDoesNotBlockOtherCallbacksOrStorage(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	msg := &llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant}
	called := false

	speech.AddItemAddedCallback(func(llm.ChatItem) {
		panic("item callback failed")
	})
	speech.AddItemAddedCallback(func(item llm.ChatItem) {
		called = item == msg
	})

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("AddChatItems panic = %v, want callback panic isolated", recovered)
		}
		if !called {
			t.Fatal("second item callback was not called after first callback panic")
		}
		items := speech.ChatItems()
		if len(items) != 1 || items[0] != msg {
			t.Fatalf("ChatItems() = %#v, want stored message after callback panic", items)
		}
	}()

	speech.AddChatItems(msg)
}

func TestSpeechHandleRemoveItemAddedCallback(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	called := false

	remove := speech.AddItemAddedCallback(func(llm.ChatItem) {
		called = true
	})
	remove()

	speech.AddChatItems(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleAssistant})

	if called {
		t.Fatal("removed item callback was called")
	}
}

func TestSpeechHandleWaitForScheduledReturnsAfterMarkScheduled(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	done := make(chan error, 1)
	go func() {
		done <- speech.WaitForScheduled(context.Background())
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForScheduled returned before MarkScheduled: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	speech.MarkScheduled()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForScheduled error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForScheduled did not return after MarkScheduled")
	}
}

func TestSpeechHandleAuthorizationCanBeClearedAndReauthorized(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	speech.AuthorizeGeneration()
	if err := speech.WaitForAuthorization(context.Background()); err != nil {
		t.Fatalf("WaitForAuthorization after AuthorizeGeneration error = %v, want nil", err)
	}

	speech.ClearAuthorization()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := speech.WaitForAuthorization(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForAuthorization after ClearAuthorization error = %v, want deadline exceeded", err)
	}

	speech.AuthorizeGeneration()
	if err := speech.WaitForAuthorization(context.Background()); err != nil {
		t.Fatalf("WaitForAuthorization after reauthorize error = %v, want nil", err)
	}
}

func TestSpeechHandleWaitForGenerationRequiresActiveGeneration(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	err := speech.WaitForGeneration(context.Background(), -1)

	if !errors.Is(err, ErrSpeechNoActiveGeneration) {
		t.Fatalf("WaitForGeneration error = %v, want ErrSpeechNoActiveGeneration", err)
	}
	if got, want := err.Error(), "cannot use wait_for_generation: no active generation is running."; got != want {
		t.Fatalf("WaitForGeneration error message = %q, want reference message %q", got, want)
	}
}

func TestSpeechHandleMarkGenerationDoneRequiresActiveGeneration(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())

	err := speech.MarkGenerationDone()

	if !errors.Is(err, ErrSpeechNoActiveGeneration) {
		t.Fatalf("MarkGenerationDone error = %v, want ErrSpeechNoActiveGeneration", err)
	}
	if got, want := err.Error(), "cannot use mark_generation_done: no active generation is running."; got != want {
		t.Fatalf("MarkGenerationDone error message = %q, want reference message %q", got, want)
	}
}

func TestSpeechHandleWaitForGenerationReturnsAfterMarkGenerationDone(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.AuthorizeGeneration()

	done := make(chan error, 1)
	go func() {
		done <- speech.WaitForGeneration(context.Background(), -1)
	}()

	select {
	case err := <-done:
		t.Fatalf("WaitForGeneration returned before MarkGenerationDone: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	if err := speech.MarkGenerationDone(); err != nil {
		t.Fatalf("MarkGenerationDone error = %v, want nil", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForGeneration error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForGeneration did not return after MarkGenerationDone")
	}
}

func TestSpeechHandleMarkDoneCompletesActiveGeneration(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.AuthorizeGeneration()

	done := make(chan error, 1)
	go func() {
		done <- speech.WaitForGeneration(context.Background(), -1)
	}()

	speech.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForGeneration error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("MarkDone did not complete active generation")
	}
}

func TestSpeechHandleMarkDoneCompletesGenerationWhenAlreadyDone(t *testing.T) {
	speech := NewSpeechHandle(true, DefaultInputDetails())
	speech.MarkDone()
	speech.AuthorizeGeneration()

	done := make(chan error, 1)
	go func() {
		done <- speech.WaitForGeneration(context.Background(), -1)
	}()

	speech.MarkDone()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForGeneration error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second MarkDone did not complete active generation")
	}
}
