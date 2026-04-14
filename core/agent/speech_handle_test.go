package agent

import (
	"context"
	"testing"
	"time"
)

func TestSpeechHandleInterruptBehavior(t *testing.T) {
	handle := NewSpeechHandle(false, DefaultInputDetails())

	if err := handle.Interrupt(false); err != nil {
		t.Fatalf("unexpected error on non-forced interrupt: %v", err)
	}
	if handle.IsInterrupted() {
		t.Fatalf("handle should not be interrupted when interruptions are disallowed")
	}

	if err := handle.Interrupt(true); err != nil {
		t.Fatalf("unexpected error on forced interrupt: %v", err)
	}
	if !handle.IsInterrupted() {
		t.Fatalf("handle should be interrupted on forced interrupt")
	}
}

func TestSpeechHandleScheduleAndWait(t *testing.T) {
	handle := NewSpeechHandle(true, DefaultInputDetails())
	handle.MarkScheduled()
	handle.MarkScheduled() // idempotent
	if !handle.IsScheduled() {
		t.Fatalf("expected handle to be scheduled")
	}

	handle.MarkDone()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := handle.Wait(ctx); err != nil {
		t.Fatalf("wait should succeed for done handle, got: %v", err)
	}
}
