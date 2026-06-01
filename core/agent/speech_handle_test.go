package agent

import (
	"context"
	"errors"
	"testing"
	"time"
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
