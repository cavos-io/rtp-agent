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
