package agent

import (
	"context"
	"testing"
	"time"
)

func newTestActivity(t *testing.T) *AgentActivity {
	t.Helper()
	base := NewAgent("test")
	session := NewAgentSession(base, nil, AgentSessionOptions{})
	return NewAgentActivity(base, session)
}

func TestScheduleSpeechPriorityOrder(t *testing.T) {
	act := newTestActivity(t)
	now := time.Now()

	normalLate := NewSpeechHandle(true, DefaultInputDetails())
	normalLate.CreatedAt = now.Add(2 * time.Second)

	high := NewSpeechHandle(true, DefaultInputDetails())
	high.CreatedAt = now.Add(1 * time.Second)

	normalEarly := NewSpeechHandle(true, DefaultInputDetails())
	normalEarly.CreatedAt = now

	if err := act.ScheduleSpeech(normalLate, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("schedule normalLate: %v", err)
	}
	if err := act.ScheduleSpeech(high, SpeechPriorityHigh, false); err != nil {
		t.Fatalf("schedule high: %v", err)
	}
	if err := act.ScheduleSpeech(normalEarly, SpeechPriorityNormal, false); err != nil {
		t.Fatalf("schedule normalEarly: %v", err)
	}

	if len(act.speechQueue) != 3 {
		t.Fatalf("unexpected queue size: got %d, want 3", len(act.speechQueue))
	}

	if act.speechQueue[0] != high {
		t.Fatalf("expected high priority speech first")
	}
	if act.speechQueue[1] != normalEarly {
		t.Fatalf("expected earlier normal priority speech second")
	}
	if act.speechQueue[2] != normalLate {
		t.Fatalf("expected later normal priority speech third")
	}
}

func TestScheduleSpeechRespectsPausedAndForce(t *testing.T) {
	act := newTestActivity(t)
	act.schedulingPaused = true

	dropped := NewSpeechHandle(true, DefaultInputDetails())
	err := act.ScheduleSpeech(dropped, SpeechPriorityNormal, false)
	if err == nil {
		t.Fatalf("expected context canceled error when scheduling is paused")
	}
	if err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dropped.IsDone() {
		t.Fatalf("expected dropped speech to be marked done")
	}

	forced := NewSpeechHandle(true, DefaultInputDetails())
	if err := act.ScheduleSpeech(forced, SpeechPriorityNormal, true); err != nil {
		t.Fatalf("expected force schedule to succeed, got: %v", err)
	}
	if len(act.speechQueue) != 1 || act.speechQueue[0] != forced {
		t.Fatalf("expected forced speech in queue")
	}
}

