package agent

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type testInterruptionDetector struct{ stream *testInterruptionStream }

type alignedInterruptionSTT struct{ fakePipelineSTT }

func (*alignedInterruptionSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: true, AlignedTranscript: "word"}
}

func (d *testInterruptionDetector) Stream(context.Context) (InterruptionStream, error) {
	return d.stream, nil
}

type testInterruptionStream struct {
	started int
	ended   int
	frames  int
	events  chan OverlappingSpeechEvent
}

func (s *testInterruptionStream) StartAgentSpeech() error           { return nil }
func (s *testInterruptionStream) EndAgentSpeech(at time.Time) error { return s.EndOverlap(at, true) }

func (s *testInterruptionStream) PushFrame(*model.AudioFrame) error { s.frames++; return nil }
func (s *testInterruptionStream) StartOverlap(time.Time) error      { s.started++; return nil }
func (s *testInterruptionStream) EndOverlap(time.Time, bool) error  { s.ended++; return nil }
func (s *testInterruptionStream) Next() (OverlappingSpeechEvent, error) {
	ev, ok := <-s.events
	if !ok {
		return OverlappingSpeechEvent{}, io.EOF
	}
	return ev, nil
}
func (s *testInterruptionStream) Close() error { close(s.events); return nil }

func TestAdaptiveInterruptionWaitsForDetectorAfterStartCooldown(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		InterruptionMode:     InterruptionModeAdaptive,
		InterruptionDetector: &testInterruptionDetector{stream: &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}},
	})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	stream := session.Options.InterruptionDetector.(*testInterruptionDetector).stream
	activity.adaptiveInterruptionStream = stream
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.OnVADInferenceDone(&vad.VADEvent{Type: vad.VADEventInferenceDone, SpeechDuration: 0.6, Speaking: true})
	if stream.started != 1 || current.IsInterrupted() || activity.pausedSpeech != nil {
		t.Fatalf("start=%d interrupted=%v paused=%v, want detector pending", stream.started, current.IsInterrupted(), activity.pausedSpeech != nil)
	}
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	if stream.ended != 1 {
		t.Fatalf("detector end calls = %d, want 1", stream.ended)
	}
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: false, DetectedAt: time.Now()})
	if current.IsInterrupted() {
		t.Fatal("backchannel interrupted agent")
	}
}

func TestAdaptiveInterruptionFailureRestoresVADAndHeldTranscript(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}
	activity.holdSTTWhileAgentSpeaking = true
	activity.heldSTTEvents = []*stt.SpeechEvent{{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "please stop", Confidence: 0.9}}}}
	activity.fallbackAdaptiveInterruption()
	if activity.adaptiveInterruptionActive() || len(activity.heldSTTEvents) != 0 || activity.holdSTTWhileAgentSpeaking {
		t.Fatal("failure did not disable adaptive gate and release held transcript")
	}
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnVADInferenceDone(&vad.VADEvent{Type: vad.VADEventInferenceDone, SpeechDuration: 0.6, Speaking: true})
	if !current.IsInterrupted() {
		t.Fatal("VAD did not interrupt after detector failure")
	}
}

func TestAdaptiveInterruptionStartCooldownStillResumesFalsePause(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, ResumeFalseInterruption: true, ResumeFalseInterruptionSet: true})
	session.SetAudioOutputController(&recordingAudioOutputController{canPause: true})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now())
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	activity.falseInterruptionMu.Lock()
	timer := activity.falseInterruptionTimer
	activity.falseInterruptionMu.Unlock()
	if timer == nil {
		t.Fatal("VAD start cooldown did not arm false interruption recovery")
	}
	timer.Stop()
}

func TestAdaptiveBackchannelDoesNotAdvanceEndpointing(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	userEvents := session.UserInputTranscribedEvents()
	activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "uh huh", Confidence: 0.9}}})
	if len(activity.heldSTTEvents) != 1 {
		t.Fatal("overlapping transcript was not held")
	}
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: false, DetectedAt: time.Now()})
	if endpointing.endCount != 1 || !endpointing.lastShouldIgnore {
		t.Fatalf("endpointing end=%d ignore=%v, want ignored backchannel", endpointing.endCount, endpointing.lastShouldIgnore)
	}
	if len(activity.heldSTTEvents) != 0 {
		t.Fatal("backchannel transcript retained")
	}
	activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "late backchannel transcript", Confidence: 0.9}}})
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	if current.IsInterrupted() {
		t.Fatal("late backchannel was treated as a new barge-in")
	}
	select {
	case ev := <-userEvents:
		t.Fatalf("backchannel transcript escaped: %#v", ev)
	default:
	}
}

func TestAdaptiveDetectorVerdictInterruptsCurrentSpeech(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer provider.Shutdown(context.Background())
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive})
	_, session.userSpeakingSpan = provider.Tracer("test").Start(context.Background(), "user_speaking")
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	stream := &testInterruptionStream{events: make(chan OverlappingSpeechEvent, 1)}
	activity.adaptiveInterruptionStream = stream
	current := NewSpeechHandle(true, DefaultInputDetails())
	activity.currentSpeech = current
	defer current.MarkDone()
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	pipeline := &PipelineAgent{session: session}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metrics := session.MetricsCollectedEvents()
	done := make(chan struct{})
	go func() {
		defer close(done)
		pipeline.consumeInterruptionStream(ctx, stream)
	}()
	stale := activity.userSpeechStartedAt.Add(-time.Second)
	stream.events <- OverlappingSpeechEvent{IsInterruption: true, DetectedAt: time.Now(), OverlapStartedAt: &stale}
	select {
	case <-current.interruptCh:
		t.Fatal("stale detector verdict interrupted current speech")
	case <-time.After(50 * time.Millisecond):
	}
	startedAt := activity.userSpeechStartedAt
	stream.events <- OverlappingSpeechEvent{IsInterruption: true, DetectedAt: startedAt, OverlapStartedAt: &startedAt}
	waitForInterrupted(t, current)
	select {
	case ev := <-metrics:
		got, ok := ev.Metrics.(*telemetry.InterruptionMetrics)
		if !ok || got.NumInterruptions != 1 {
			t.Fatalf("metrics = %#v, want one interruption", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("no interruption metrics emitted")
	}
	_ = stream.Close()
	<-done
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	spans := recorder.Ended()
	if len(spans) != 1 || spanAttributeValues(spans[0].Attributes())[telemetry.AttrIsInterruption].AsString() != "true" {
		t.Fatalf("detector interruption missing from exported user_speaking span: %v", spans)
	}
}

func TestAdaptiveAgentSpeechEndReleasesUnresolvedUserTurn(t *testing.T) {
	endpointing := &recordingActivityEndpointing{}
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, Endpointing: endpointing})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	activity.onAgentSpeechEnded(time.Now())
	if endpointing.endCount != 1 || endpointing.lastShouldIgnore {
		t.Fatalf("agent end left user endpointing unresolved: count=%d ignored=%v", endpointing.endCount, endpointing.lastShouldIgnore)
	}
}

func TestAdaptiveActivityUsesStreamStartedAfterHandoffConstruction(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive})
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	session.Assistant = pipeline
	activity := NewAgentActivity(agent, session)
	pipeline.interruptionStream.Store(&testInterruptionStream{events: make(chan OverlappingSpeechEvent)})
	if !activity.adaptiveInterruptionActive() {
		t.Fatal("handoff activity did not see replacement pipeline stream")
	}
}

func TestAdaptiveNilDetectorStreamFallsBackToVAD(t *testing.T) {
	agent := NewAgent("test")
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, InterruptionDetector: &testInterruptionDetector{}})
	pipeline := NewPipelineAgent(nil, nil, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pipeline.Start(ctx, session); err != nil {
		t.Fatal(err)
	}
	if !pipeline.adaptiveFailed.Load() {
		t.Fatal("nil detector stream did not fall back to VAD")
	}
}

func TestAdaptiveInterruptionRejectsDisabledInterruptions(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &alignedInterruptionSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{
		InterruptionMode:     InterruptionModeAdaptive,
		InterruptionDetector: &testInterruptionDetector{},
		AllowInterruptions:   false, AllowInterruptionsSet: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err == nil {
		_ = session.Stop(ctx)
		t.Fatal("adaptive mode started while interruptions were disabled")
	}
}

func TestAdaptiveInterruptionRejectsManualTurnDetection(t *testing.T) {
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &alignedInterruptionSTT{}
	agent.TurnDetection = TurnDetectionModeManual
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, InterruptionDetector: &testInterruptionDetector{}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := session.Start(ctx); err == nil {
		_ = session.Stop(ctx)
		t.Fatal("adaptive mode started with manual turn detection")
	}
}

func TestAdaptiveInterruptionRejectsMissingCapabilities(t *testing.T) {
	cases := []struct {
		name     string
		vad      vad.VAD
		stt      stt.STT
		detector InterruptionDetector
	}{
		{"detector", &fakePipelineVAD{}, &fakePipelineSTT{}, nil},
		{"vad", nil, &fakePipelineSTT{}, &testInterruptionDetector{}},
		{"stt", &fakePipelineVAD{}, nil, &testInterruptionDetector{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateAdaptiveInterruption(AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, InterruptionDetector: tc.detector}, tc.vad, tc.stt); err == nil {
				t.Fatal("invalid adaptive configuration accepted")
			}
		})
	}
	if err := validateAdaptiveInterruption(AgentSessionOptions{InterruptionMode: InterruptionModeVAD}, nil, nil); err != nil {
		t.Fatalf("VAD default rejected: %v", err)
	}
}

func TestAdaptiveUserSpeakingSpanVerdict(t *testing.T) {
	for _, verdict := range []string{"false", "true", "agent_ended"} {
		t.Run(verdict, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer provider.Shutdown(context.Background())
			session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive})
			activity := NewAgentActivity(session.Agent, session)
			activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent, 1)}
			_, session.userSpeakingSpan = provider.Tracer("test").Start(context.Background(), "user_speaking")
			session.userState = UserStateSpeaking
			activity.adaptiveOverlapPending.Store(true)
			if verdict == "true" {
				activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: true})
				// A late negative update must never replace the positive verdict.
				session.markUserSpeakingInterruption(false)
			} else if verdict == "agent_ended" {
				activity.onAgentSpeechEnded(time.Now())
			}
			activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("ended spans = %d", len(spans))
			}
			attrs := spanAttributeValues(spans[0].Attributes())
			got, present := attrs[telemetry.AttrIsInterruption]
			if verdict == "agent_ended" {
				if present {
					t.Fatal("inconclusive overlap labeled as backchannel")
				}
			} else if !present || got.AsString() != verdict {
				t.Fatalf("verdict = %v, want %s", got, verdict)
			}
		})
	}
}

func TestAdaptiveAgentEndedUsageIsNotBackchannel(t *testing.T) {
	session := NewAgentSession(NewAgent("test"), nil, AgentSessionOptions{})
	stream := &testInterruptionStream{events: make(chan OverlappingSpeechEvent, 1)}
	ended := OverlappingSpeechEvent{AgentEnded: true, NumRequests: 7, DetectedAt: time.Now()}
	payload, err := json.Marshal(&ended)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["agent_ended"] != true || sessionReportEventToDict(&ended)["agent_ended"] != true {
		t.Fatal("inconclusive marker missing from event or report")
	}
	stream.events <- ended
	close(stream.events)
	metrics := session.MetricsCollectedEvents()
	pipeline := &PipelineAgent{session: session}
	pipeline.consumeInterruptionStream(context.Background(), stream)
	select {
	case ev := <-metrics:
		got := ev.Metrics.(*telemetry.InterruptionMetrics)
		if got.NumRequests != 7 || got.NumBackchannels != 0 || got.NumInterruptions != 0 {
			t.Fatalf("inconclusive usage = %#v", got)
		}
	default:
		t.Fatal("agent-ended requests were lost")
	}
}

// Unlike the pinned Python reference, confirmed backchannels close their provisional turns.
func TestAdaptiveBackchannelClosesUserTurn(t *testing.T) {
	activity, recorder := newBackchannelTurnActivity(t)
	base := time.Now().Add(-30 * time.Second)
	var starts, ends []time.Time
	for _, offset := range []time.Duration{15157 * time.Millisecond, 18464 * time.Millisecond} {
		started := timeToUnixSeconds(base.Add(offset))
		activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &started})
		starts = append(starts, activity.userSpeechStartedAt)
		activity.userSpeechStoppedAt = activity.userSpeechStartedAt.Add(300 * time.Millisecond)
		ends = append(ends, activity.userSpeechStoppedAt)
		activity.OnEndOfSpeech(nil)
		activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{OverlapStartedAt: &starts[len(starts)-1], DetectedAt: ends[len(ends)-1]})
		if activity.Session.userTurnSpan != nil || !activity.userTurnStartedAt.IsZero() {
			t.Fatal("confirmed backchannel retained its turn span or start timestamp")
		}
		activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "uh huh"}}})
	}
	started := timeToUnixSeconds(base.Add(21624 * time.Millisecond))
	activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &started})
	starts = append(starts, activity.userSpeechStartedAt)
	activity.OnOverlapSpeechEnded(OverlappingSpeechEvent{IsInterruption: true, OverlapStartedAt: &starts[2], DetectedAt: starts[2]})
	activity.userSpeechStoppedAt = base.Add(23378 * time.Millisecond)
	ends = append(ends, activity.userSpeechStoppedAt)
	activity.OnEndOfSpeech(nil)
	activity.userTurnMu.Lock()
	activity.holdSTTWhileAgentSpeaking = false
	activity.userTurnMu.Unlock()
	activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "Okay, I think that's enough.", Confidence: 1}}})
	info := activity.pendingFinalEndOfTurnInfo()
	if info.StartedSpeakingAt == nil || *info.StartedSpeakingAt != started {
		t.Fatalf("report start = %v, want %v", info.StartedSpeakingAt, started)
	}
	if info.NewTranscript != "Okay, I think that's enough." {
		t.Fatalf("accepted transcript = %q", info.NewTranscript)
	}
	activity.Session.finishUserTurnSpan(activity.Session.userTurnSpan)
	var turns, speaking []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case "user_turn":
			turns = append(turns, span)
		case "user_speaking":
			speaking = append(speaking, span)
		}
	}
	if len(turns) != 3 || len(speaking) != 3 {
		t.Fatalf("turns=%d speaking=%d, want three of each", len(turns), len(speaking))
	}
	for i := range turns {
		if !turns[i].StartTime().Equal(starts[i]) || speaking[i].Parent().SpanID() != turns[i].SpanContext().SpanID() {
			t.Fatalf("turn %d has wrong start or speaking child", i)
		}
		if i < 2 && (!turns[i].EndTime().Equal(ends[i]) || spanAttributeValues(speaking[i].Attributes())[telemetry.AttrIsInterruption].AsString() != "false") {
			t.Fatalf("backchannel %d has wrong end or verdict", i)
		}
	}
}

func TestAdaptiveBackchannelTurnVerdictOrdering(t *testing.T) {
	for _, scenario := range []string{"early", "stale", "duplicate", "accepted transcript", "agent ended"} {
		t.Run(scenario, func(t *testing.T) {
			activity, _ := newBackchannelTurnActivity(t)
			activity.OnStartOfSpeech(nil)
			started := activity.userSpeechStartedAt
			span := activity.Session.userTurnSpan
			verdict := OverlappingSpeechEvent{OverlapStartedAt: &started, DetectedAt: time.Now()}
			if scenario == "early" {
				activity.OnOverlapSpeechEnded(verdict)
				if activity.Session.userTurnSpan != span {
					t.Fatal("closed turn while its speaking span was open")
				}
				activity.OnEndOfSpeech(nil)
				if activity.Session.userTurnSpan != nil {
					t.Fatal("early verdict did not close turn at speech end")
				}
				return
			}
			activity.OnEndOfSpeech(nil)
			switch scenario {
			case "stale", "duplicate":
				if scenario == "duplicate" {
					activity.OnOverlapSpeechEnded(verdict)
				}
				activity.OnStartOfSpeech(nil)
				span = activity.Session.userTurnSpan
			case "accepted transcript":
				activity.pendingUserTranscript = "keep this"
				activity.pendingUserTranscriptPresent = true
			case "agent ended":
				verdict.AgentEnded = true
			}
			anchor := activity.userTurnStartedAt
			activity.OnOverlapSpeechEnded(verdict)
			if activity.Session.userTurnSpan != span || !activity.userTurnStartedAt.Equal(anchor) {
				t.Fatal("verdict reset an unrelated or accepted turn")
			}
			if scenario == "accepted transcript" && activity.pendingUserTranscript != "keep this" {
				t.Fatal("accepted transcript was cleared")
			}
		})
	}
}

func newBackchannelTurnActivity(t *testing.T) (*AgentActivity, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	observability, err := telemetry.NewJobObservability(context.Background(), telemetry.JobObservabilityConfig{
		TracerProvider: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = observability.Shutdown(context.Background()) })
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, TurnDetection: TurnDetectionModeManual, DisableUserAwayTimeout: true})
	session.runCtx = observability.Context(context.Background())
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}
	session.agentState = AgentStateSpeaking
	activity.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	return activity, recorder
}

func TestAdaptiveBackchannelConcurrentNextSpeech(t *testing.T) {
	activity, _ := newBackchannelTurnActivity(t)
	activity.OnStartOfSpeech(nil)
	oldStart := activity.userSpeechStartedAt
	activity.OnEndOfSpeech(nil)
	verdict := OverlappingSpeechEvent{OverlapStartedAt: &oldStart, DetectedAt: activity.userSpeechStoppedAt}
	newStart := timeToUnixSeconds(oldStart.Add(time.Second))
	ready := make(chan struct{})
	done := make(chan struct{}, 2)
	go func() {
		<-ready
		activity.OnOverlapSpeechEnded(verdict)
		done <- struct{}{}
	}()
	go func() {
		<-ready
		activity.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &newStart})
		done <- struct{}{}
	}()
	close(ready)
	<-done
	<-done
	if activity.Session.userTurnSpan == nil || !activity.isUserSpeaking() || activity.userSpeechStartedAt != unixSecondsToTime(newStart) {
		t.Fatal("old backchannel verdict cleared newer speech")
	}
}
