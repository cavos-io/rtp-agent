package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestFalseInterruptionFreshTurnTiming(t *testing.T) {
	recorder := recordFalseInterruptionSpans(t)

	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		ResumeFalseInterruption: true, ResumeFalseInterruptionSet: true,
	})
	output := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(output)
	session.agentState = AgentStateThinking
	activity := NewAgentActivity(agent, session)
	agent.activity, session.activity = activity, activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	t.Cleanup(func() { activity.cancelFalseInterruptionTimer(); current.MarkDone(); activity.Stop() })

	// Historical timestamps bypass the recognition grace period. Only the
	// resume event advances the test; wall-clock delays do not drive it.
	base := time.Now().Add(-time.Minute).Truncate(time.Second)
	start := func(offset time.Duration) {
		at := timeToUnixSeconds(base.Add(offset))
		activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &at})
	}
	end := func(offset time.Duration) {
		activity.userSpeechStoppedAt = base.Add(offset)
		activity.OnEndOfSpeech(nil)
	}
	start(11377 * time.Millisecond)
	end(11495 * time.Millisecond)
	select {
	case ev := <-session.AgentFalseInterruptionEvents():
		if !ev.Resumed {
			t.Fatal("thinking reply did not resume")
		}
	case <-time.After(time.Second):
		t.Fatal("false interruption did not resolve")
	}
	current.MarkDone()
	start(27999 * time.Millisecond)
	end(32602 * time.Millisecond)
	start(33821 * time.Millisecond)
	end(34350 * time.Millisecond)
	const transcript = "Enggak ada kendala sih, kemarin sudah deposit juga."
	activity.OnFinalTranscript(&stt.SpeechEvent{
		Type:         stt.SpeechEventFinalTranscript,
		Alternatives: []stt.SpeechData{{Text: transcript, Confidence: 0.9}},
	})
	if _, err := activity.CommitUserTurn(context.Background(), CommitUserTurnOptions{}); err != nil {
		t.Fatal(err)
	}
	items := agent.ChatCtx.Items
	if len(items) != 1 {
		t.Fatalf("chat items = %d, want one genuine user message", len(items))
	}
	msg, _ := items[0].(*llm.ChatMessage)
	if msg == nil {
		t.Fatal("committed item is not a message")
	}
	if msg.TextContent() != transcript {
		t.Fatalf("committed transcript = %q", msg.TextContent())
	}
	if got, want := msg.Metrics["stopped_speaking_at"], timeToUnixSeconds(base.Add(34350*time.Millisecond)); got != want {
		t.Errorf("stopped_speaking_at = %v, want %v", got, want)
	}
	if got, want := msg.Metrics["started_speaking_at"], timeToUnixSeconds(base.Add(27999*time.Millisecond)); got != want {
		t.Errorf("started_speaking_at = %v, want first genuine segment %v", got, want)
	}
	var turns, speaking []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case "user_turn":
			turns = append(turns, span)
		case "user_speaking":
			speaking = append(speaking, span)
		}
	}
	if len(turns) != 2 || len(speaking) != 3 {
		t.Fatalf("trace has %d turns and %d speaking spans, want 2 and 3", len(turns), len(speaking))
	}
	if !turns[0].StartTime().Equal(unixSecondsToTime(timeToUnixSeconds(base.Add(11377*time.Millisecond)))) || !turns[1].StartTime().Equal(unixSecondsToTime(timeToUnixSeconds(base.Add(27999*time.Millisecond)))) {
		t.Fatal("turn boundaries do not separate candidate from genuine speech")
	}
	if turns[0].EndTime().After(turns[1].StartTime()) || turns[0].EndTime().Before(speaking[0].EndTime()) {
		t.Fatal("candidate end is outside its resolved boundary")
	}
	for i, span := range speaking {
		turn := turns[1]
		if i == 0 {
			turn = turns[0]
		}
		if span.Parent().SpanID() != turn.SpanContext().SpanID() {
			t.Fatalf("speaking span %d belongs to wrong turn", i)
		}
	}
}

func recordFalseInterruptionSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	oldTracer := telemetry.Tracer
	telemetry.Tracer = provider.Tracer("test")
	t.Cleanup(func() {
		telemetry.Tracer = oldTracer
		_ = provider.Shutdown(context.Background())
	})

	return recorder
}

// Arm a long timer and resolve it explicitly so preservation/order tests never
// depend on the scheduler. The main regression above exercises the zero timer.
func newFalseInterruptionTimingActivity(t *testing.T) (*AgentActivity, *recordingAudioOutputController, time.Time) {
	t.Helper()
	recordFalseInterruptionSpans(t)
	agent := NewAgent("test")
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{ResumeFalseInterruption: true, ResumeFalseInterruptionSet: true})
	output := &recordingAudioOutputController{canPause: true}
	session.SetAudioOutputController(output)
	session.agentState = AgentStateThinking
	activity := NewAgentActivity(agent, session)
	agent.activity, session.activity = activity, activity
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	t.Cleanup(func() { activity.cancelFalseInterruptionTimer(); current.MarkDone(); activity.Stop() })
	started := time.Now().Add(-time.Minute).Truncate(time.Second)
	at := timeToUnixSeconds(started)
	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &at})
	activity.falseInterruptionMu.Lock()
	activity.pausedSpeech.timeout = time.Hour
	activity.falseInterruptionMu.Unlock()
	activity.userSpeechStoppedAt = started.Add(time.Second)
	activity.OnEndOfSpeech(nil)
	activity.cancelFalseInterruptionTimer()
	return activity, output, started
}

func TestFalseInterruptionPreservesAcceptedText(t *testing.T) {
	for _, kind := range []stt.SpeechEventType{stt.SpeechEventFinalTranscript, stt.SpeechEventInterimTranscript, stt.SpeechEventPreflightTranscript} {
		for _, late := range []bool{false, true} {
			name := string(kind) + "/before_resume"
			if late {
				name = string(kind) + "/after_resume"
			}
			t.Run(name, func(t *testing.T) {
				activity, _, started := newFalseInterruptionTimingActivity(t)
				if late {
					activity.resumeFalseInterruption()
				}
				event := &stt.SpeechEvent{Type: kind, Alternatives: []stt.SpeechData{{Text: "genuine delayed speech", Confidence: 0.9}}}
				if kind == stt.SpeechEventFinalTranscript {
					activity.OnFinalTranscript(event)
				} else {
					activity.OnInterimTranscript(event)
				}
				if !late {
					activity.resumeFalseInterruption()
				}
				originalSpan := activity.Session.userTurnSpan
				activity.currentSpeech.MarkDone()
				next := timeToUnixSeconds(started.Add(10 * time.Second))
				activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &next})
				if activity.Session.userTurnSpan.SpanContext().SpanID() != originalSpan.SpanContext().SpanID() {
					t.Fatal("accepted text lost its original trace")
				}
				if kind != stt.SpeechEventFinalTranscript {
					activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: event.Alternatives})
				}
				info := activity.pendingFinalEndOfTurnInfo()
				if info.StartedSpeakingAt == nil || *info.StartedSpeakingAt != timeToUnixSeconds(started) {
					t.Fatalf("accepted text onset = %v, want %v", info.StartedSpeakingAt, started)
				}
				if info.NewTranscript != "genuine delayed speech" {
					t.Fatalf("pending transcript = %q", info.NewTranscript)
				}
			})
		}
	}
}

func TestFalseInterruptionStaleTimerCannotResumeNewSpeech(t *testing.T) {
	activity, output, started := newFalseInterruptionTimingActivity(t)
	activity.falseInterruptionMu.Lock()
	paused, timerSeq, speechSeq := activity.pausedSpeech, activity.falseInterruptionTimerSeq, activity.userTurnSeq
	activity.falseInterruptionMu.Unlock()
	ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		close(ready)
		<-release
		activity.resumeFalseInterruptionFor(paused, timerSeq, speechSeq)
		close(done)
	}()
	<-ready
	next := timeToUnixSeconds(started.Add(10 * time.Second))
	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &next})
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale timer blocked")
	}
	if output.resumeCount != 0 {
		t.Fatalf("stale timer resumed output %d times", output.resumeCount)
	}
	if activity.resolvedEmptyTurn != nil {
		t.Fatal("stale timer marked newer speech as empty")
	}
	if activity.pausedSpeech != paused {
		t.Fatal("stale timer took ownership of the current pause")
	}
}

func TestFalseInterruptionCanceledTimerCannotResolveSameEpoch(t *testing.T) {
	activity, output, _ := newFalseInterruptionTimingActivity(t)
	activity.falseInterruptionMu.Lock()
	paused, timerSeq, speechSeq := activity.pausedSpeech, activity.falseInterruptionTimerSeq, activity.userTurnSeq
	activity.falseInterruptionMu.Unlock()
	activity.startFalseInterruptionTimer()
	activity.resumeFalseInterruptionFor(paused, timerSeq, speechSeq)
	if output.resumeCount != 0 || activity.pausedSpeech != paused {
		t.Fatal("old timer resolved a replacement timer's pause")
	}
	activity.cancelFalseInterruptionTimer()
	activity.resumeFalseInterruption()
	if output.resumeCount != 1 {
		t.Fatalf("current resolution resumed %d times, want 1", output.resumeCount)
	}
}

func TestFalseInterruptionResumeAllowsHeldSpeechOnset(t *testing.T) {
	activity, output, started := newFalseInterruptionTimingActivity(t)
	// Playback can become speaking while the earlier thinking reply is paused.
	// Restoring thinking flushes held STT, which can start a new speech epoch.
	activity.Session.UpdateAgentState(AgentStateSpeaking)
	next := timeToUnixSeconds(started.Add(10 * time.Second))
	activity.userTurnMu.Lock()
	activity.heldSTTEvents = []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech, SpeechStartTime: &next}}
	activity.userTurnMu.Unlock()
	done := make(chan struct{})
	go func() { activity.resumeFalseInterruption(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("resume deadlocked while flushing held speech onset")
	}
	if !activity.isUserSpeaking() {
		t.Fatal("held speech onset was lost")
	}
	if output.resumeCount != 0 {
		t.Fatal("old resolution resumed over the held speech onset")
	}
}
