package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

func TestSpeechHandleInterruptDisallowedReturnsError(t *testing.T) {
	speech := NewSpeechHandle(false, DefaultInputDetails())

	err := speech.Interrupt(false)

	if !errors.Is(err, ErrSpeechInterruptionsDisabled) {
		t.Fatalf("Interrupt(false) error = %v, want ErrSpeechInterruptionsDisabled", err)
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
	if !speech.AllowInterruptions {
		t.Fatal("AllowInterruptions changed to false after interruption, want unchanged")
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

	if !errors.Is(err, ErrSpeechInterrupted) {
		t.Fatalf("WaitIfNotInterrupted error = %v, want ErrSpeechInterrupted", err)
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
