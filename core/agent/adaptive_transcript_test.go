package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestAdaptiveAcceptsUnalignedSTT(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		provider := &unalignedTranscriptSTT{streaming: streaming}
		err := validateAdaptiveInterruption(AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, InterruptionDetector: &testInterruptionDetector{}}, &fakePipelineVAD{}, provider)
		if err != nil {
			t.Fatalf("streaming=%v: %v", streaming, err)
		}
	}
}

func newAdaptiveTranscriptActivity(t *testing.T) *AgentActivity {
	t.Helper()
	agent := NewAgent("test")
	agent.VAD = &fakePipelineVAD{}
	agent.STT = &fakePipelineSTT{}
	session := NewAgentSession(agent, nil, AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, DisableUserAwayTimeout: true, DisableAgentSpeakingStallTimeout: true})
	activity := NewAgentActivity(agent, session)
	session.activity = activity
	activity.adaptiveInterruptionStream = &testInterruptionStream{events: make(chan OverlappingSpeechEvent, 8)}
	session.agentState = AgentStateSpeaking
	return activity
}

func TestAdaptiveAgentSpeechDoesNotGateWithoutOverlap(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	if a.holdSTTWhileAgentSpeaking {
		t.Fatal("agent speech alone enabled transcript gate")
	}
}

type unalignedTranscriptSTT struct {
	fakePipelineSTT
	streaming bool
}

func (s *unalignedTranscriptSTT) Capabilities() stt.STTCapabilities {
	return stt.STTCapabilities{Streaming: s.streaming}
}

func TestAdaptiveTranscriptTrimUsesAbsoluteTimingOrReceipt(t *testing.T) {
	cases := []struct {
		name                 string
		agent, vad, resolved float64
		events               []*stt.SpeechEvent
		want                 []string
	}{
		{"receipt", 2, 0, 10, []*stt.SpeechEvent{timedGateEvent("old", 8, nil), timedGateEvent("recent", 9.5, nil)}, []string{"recent"}},
		{"vad onset", 2, 5, 10, []*stt.SpeechEvent{timedGateEvent("old", 4, nil), timedGateEvent("start", 5, nil), timedGateEvent("end", 9.5, nil)}, []string{"start", "end"}},
		{"agent clamp", 8, 5, 10, []*stt.SpeechEvent{timedGateEvent("old", 7.5, nil), timedGateEvent("start", 8, nil)}, []string{"start"}},
		{"delayed prior speech", 8, 0, 10, []*stt.SpeechEvent{timedGateEvent("prior", 8.5, floatPtr(7.5))}, []string{"prior"}},
		{"delayed backchannel", 8, 0, 10, []*stt.SpeechEvent{timedGateEvent("backchannel", 9.5, floatPtr(8.5))}, nil},
		{"provider order", 2, 0, 10, []*stt.SpeechEvent{timedGateEvent("keep", 9.5, nil), timedGateEvent("suffix", 8, nil)}, []string{"keep", "suffix"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAdaptiveTranscriptActivity(t)
			a.adaptiveAgentSpeechStartedAt = unixSecondsToTime(tc.agent)
			a.heldSTTEvents = tc.events
			a.trimAdaptiveTranscripts(unixSecondsToTime(tc.resolved), optionalGateTime(tc.vad))
			if len(a.heldSTTEvents) != len(tc.want) {
				t.Fatalf("retained %d, want %v", len(a.heldSTTEvents), tc.want)
			}
			for i, e := range a.heldSTTEvents {
				if e.RequestID != tc.want[i] {
					t.Fatalf("event %d=%s want %s", i, e.RequestID, tc.want[i])
				}
			}
		})
	}
}
func floatPtr(v float64) *float64 { return &v }
func optionalGateTime(v float64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return unixSecondsToTime(v)
}
func timedGateEvent(id string, received float64, end *float64) *stt.SpeechEvent {
	return &stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, RequestID: id, ReceivedAt: unixSecondsToTime(received), SpeechEndTime: end, Alternatives: []stt.SpeechData{{Text: id, Confidence: 1}}}
}

func TestAdaptiveOverlapRearmsAndCooldownKeepsVAD(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	base := time.Now().Add(-10 * time.Second)
	a.armBackchannelBoundary(base)
	if !a.startAdaptiveOverlap(base.Add(2*time.Second)) || !a.holdSTTWhileAgentSpeaking {
		t.Fatal("overlap did not gate")
	}
	a.releaseAdaptiveTranscripts(time.Time{}, time.Time{})
	if !a.startAdaptiveOverlap(base.Add(3 * time.Second)) {
		t.Fatal("next overlap did not rearm")
	}
	a.armBackchannelBoundary(base)
	if a.startAdaptiveOverlap(base.Add(500 * time.Millisecond)) {
		t.Fatal("historical cooldown overlap used detector")
	}
	if a.startAdaptiveOverlap(base.Add(3*time.Second)) || a.audioActivityInterruptionDisabled(time.Now()) {
		t.Fatal("VAD ownership did not persist")
	}
}

func TestAdaptiveUntimedReleaseKeepsReceiptAndUserSpeech(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.Options.TurnDetection = TurnDetectionModeSTT
	a.armBackchannelBoundary(unixSecondsToTime(2))
	a.holdSTTWhileAgentSpeaking = true
	events := a.Session.UserInputTranscribedEvents()
	old, recent := timedGateEvent("old", 8, nil), timedGateEvent("please stop", 9.5, nil)
	a.OnFinalTranscript(old)
	a.OnFinalTranscript(recent)
	a.releaseAdaptiveTranscripts(unixSecondsToTime(10), time.Time{})
	if a.holdSTTWhileAgentSpeaking || len(a.heldSTTEvents) != 0 {
		t.Fatal("release re-buffered events")
	}
	select {
	case ev := <-events:
		if ev.Transcript != "please stop" {
			t.Fatalf("emitted %q", ev.Transcript)
		}
	default:
		t.Fatal("lost interruption")
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected transcript %q", ev.Transcript)
	default:
	}
	if !recent.ReceivedAt.Equal(unixSecondsToTime(9.5)) {
		t.Fatal("receipt time changed")
	}
}

func TestAdaptiveUngatedFinalRestoresVAD(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
	a.OnFinalTranscript(timedGateEvent("late", timeToUnixSeconds(time.Now()), nil))
	if a.audioActivityInterruptionDisabled(time.Now()) {
		t.Fatal("ungated final did not restore VAD")
	}
}

func TestAdaptiveDisabledInterruptionsReleaseGate(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.holdSTTWhileAgentSpeaking = true
	a.OnFinalTranscript(timedGateEvent("held", timeToUnixSeconds(time.Now()), nil))
	a.Session.Options.AllowInterruptions = false
	a.Session.Options.AllowInterruptionsSet = true
	a.OnFinalTranscript(timedGateEvent("new", timeToUnixSeconds(time.Now()), nil))
	if len(a.heldSTTEvents) != 0 || a.holdSTTWhileAgentSpeaking {
		t.Fatal("disable left transcript gate active")
	}
}

func TestAdaptiveSTTIngressOnlyDerivesAlignedTiming(t *testing.T) {
	for _, aligned := range []bool{false, true} {
		t.Run(fmt.Sprint(aligned), func(t *testing.T) {
			a := newAdaptiveTranscriptActivity(t)
			var provider stt.STT = &fakePipelineSTT{}
			if aligned {
				provider = &alignedInterruptionSTT{}
			}
			va := NewPipelineAgent(nil, provider, nil, nil, nil)
			va.session = a.Session
			va.ctx = context.Background()
			a.holdSTTWhileAgentSpeaking = true
			stream := &fakePipelineRecognizeStream{startTime: 100, events: []*stt.SpeechEvent{timedGateEvent("hello", 102, nil)}}
			stream.events[0].Alternatives[0].EndTime = 1
			va.sttLoop(stream)
			if len(a.heldSTTEvents) != 1 {
				t.Fatalf("held=%d", len(a.heldSTTEvents))
			}
			got := a.heldSTTEvents[0]
			if aligned {
				if got.SpeechEndTime == nil || *got.SpeechEndTime != 101 {
					t.Fatalf("end=%v", got.SpeechEndTime)
				}
			} else if got.SpeechEndTime != nil {
				t.Fatal("unaligned STT acquired synthetic timing")
			}
			if !got.ReceivedAt.Equal(unixSecondsToTime(102)) {
				t.Fatal("receipt replaced")
			}
		})
	}
}

// Shared JSON drives real Go methods and the pinned Python method bodies.
func TestAdaptiveTranscriptReferenceScenario(t *testing.T) {
	filename := os.Getenv("ADAPTIVE_TRANSCRIPT_SCENARIO")
	if filename == "" {
		filename = "../../scripts/parity-fixtures/adaptive-transcript.json"
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var scenario struct {
		Contract string `json:"contract"`
		Cases    []struct {
			Name     string  `json:"name"`
			Action   string  `json:"action"`
			Agent    float64 `json:"agent"`
			VAD      float64 `json:"vad"`
			Resolved float64 `json:"resolved"`
			Overlap  float64 `json:"overlap"`
			Events   []struct {
				ID       string   `json:"id"`
				Received float64  `json:"received"`
				End      *float64 `json:"end"`
			} `json:"events"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &scenario); err != nil {
		t.Fatal(err)
	}
	results := []map[string]any{}
	for _, c := range scenario.Cases {
		a := newAdaptiveTranscriptActivity(t)
		a.Session.Options.BackchannelBoundaryStart = 0
		a.adaptiveAgentSpeechStartedAt = optionalGateTime(c.Agent)
		a.holdSTTWhileAgentSpeaking = true
		events := a.Session.UserInputTranscribedEvents()
		for _, e := range c.Events {
			a.heldSTTEvents = append(a.heldSTTEvents, timedGateEvent(e.ID, e.Received, e.End))
		}
		switch c.Action {
		case "trim":
			a.trimAdaptiveTranscripts(unixSecondsToTime(c.Resolved), optionalGateTime(c.VAD))
		case "release":
			a.releaseAdaptiveTranscripts(unixSecondsToTime(c.Resolved), optionalGateTime(c.VAD))
		case "interruption", "backchannel":
			onset := unixSecondsToTime(c.Overlap)
			a.userSpeechStartedAt = onset
			a.setSpeaking(true)
			a.adaptiveOverlapStartedAt.Store(onset.UnixNano())
			a.adaptiveOverlapPending.Store(true)
			verdict := OverlappingSpeechEvent{IsInterruption: c.Action == "interruption", DetectedAt: unixSecondsToTime(c.Resolved), OverlapStartedAt: &onset}
			if !a.onOverlapSpeechEnded(verdict) {
				t.Fatal("reference scenario verdict rejected")
			}
			if verdict.IsInterruption {
				a.OnInterruption(verdict)
			}
		case "start":
			a.armBackchannelBoundary(unixSecondsToTime(c.Agent))
		case "rearm":
			a.holdSTTWhileAgentSpeaking = false
			a.startAdaptiveOverlap(unixSecondsToTime(c.Resolved))
		case "cooldown":
			a.Session.Options.BackchannelBoundaryStart = 1
			a.holdSTTWhileAgentSpeaking = false
			a.startAdaptiveOverlap(unixSecondsToTime(c.Agent + .5))
			a.startAdaptiveOverlap(unixSecondsToTime(c.Agent + 2))
		default:
			t.Fatalf("unknown action %s", c.Action)
		}
		held, emitted := []string{}, []string{}
		for _, e := range a.heldSTTEvents {
			held = append(held, e.RequestID)
		}
		for len(events) > 0 {
			emitted = append(emitted, (<-events).Transcript)
		}
		results = append(results, map[string]any{"name": c.Name, "held": held, "emitted": emitted, "gate": a.holdSTTWhileAgentSpeaking})
	}
	out, err := json.Marshal(map[string]any{"contract": scenario.Contract, "events": results})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("ADAPTIVE_TRACE %s\n", out)
}

func TestAdaptiveAgentResumeReleasesHeldSpeechOutsideSessionLock(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.agentState = AgentStateListening
	a.setSpeaking(true)
	a.heldSTTEvents = []*stt.SpeechEvent{timedGateEvent("answer", timeToUnixSeconds(time.Now()), nil)}
	done := make(chan struct{})
	go func() { a.Session.UpdateAgentState(AgentStateSpeaking); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("agent resume deadlocked replaying held transcript")
	}
	if len(a.heldSTTEvents) != 0 {
		t.Fatal("start cooldown did not release held transcript")
	}
}

func TestAdaptiveSTTBoundaryReplayDoesNotRearmOverlap(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.Options.TurnDetection = TurnDetectionModeSTT
	a.Session.Options.MinEndpointingDelay = 60
	a.Session.Options.MaxEndpointingDelay = 60
	a.interruptionDetected = true
	a.holdSTTWhileAgentSpeaking = true
	onset := timeToUnixSeconds(time.Now())
	a.heldSTTEvents = []*stt.SpeechEvent{{Type: stt.SpeechEventStartOfSpeech, SpeechStartTime: &onset}, timedGateEvent("please stop", onset, nil), {Type: stt.SpeechEventEndOfSpeech}}
	a.releaseAdaptiveTranscripts(time.Time{}, time.Time{})
	if a.holdSTTWhileAgentSpeaking || a.adaptiveOverlapPending.Load() || !a.interruptionDetected {
		t.Fatal("replay rearmed or reset detector verdict")
	}
	if a.pendingFinalEndOfTurnInfo().NewTranscript != "please stop" {
		t.Fatal("lost replayed transcript")
	}
	a.cancelPendingEOUDetection()
}

func TestAdaptiveUnalignedBatchSTTUsesStreamAdapter(t *testing.T) {
	provider := &nonStreamingPipelineSTT{}
	vadModel := &fakePipelineVAD{}
	options := AgentSessionOptions{InterruptionMode: InterruptionModeAdaptive, InterruptionDetector: &testInterruptionDetector{}}
	if err := validateAdaptiveInterruption(options, vadModel, provider); err != nil {
		t.Fatal(err)
	}
	wrapped, err := streamableSTT(provider, vadModel)
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := wrapped.(*stt.StreamAdapter)
	if !ok || adapter.WrappedSTT() != provider || !adapter.Capabilities().Streaming || adapter.Capabilities().AlignedTranscript != "" {
		t.Fatal("batch STT was not adapted without fabricating alignment")
	}
	stream, err := wrapped.Stream(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAdaptiveAgentStartsDuringUserSpeechAcceptsOverlapVerdict(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.Options.BackchannelBoundaryStart = 0
	a.setSpeaking(true)
	a.userSpeechStartedAt = time.Now().Add(-time.Second)
	agentStart := time.Now()
	a.armBackchannelBoundary(agentStart)
	if !a.onOverlapSpeechEnded(OverlappingSpeechEvent{OverlapStartedAt: &agentStart, DetectedAt: agentStart.Add(time.Second), IsInterruption: true}) {
		t.Fatal("valid overlap starting at agent playout was rejected against earlier user onset")
	}
}

func TestAdaptiveSessionStartsWithUnalignedSTT(t *testing.T) {
	for _, provider := range []stt.STT{&fakePipelineSTT{}, &nonStreamingPipelineSTT{}} {
		t.Run(provider.Label(), func(t *testing.T) {
			agent := NewAgent("unaligned")
			agent.VAD = &fakePipelineVAD{}
			agent.STT = provider
			session := NewAgentSession(agent, nil, AgentSessionOptions{
				InterruptionMode:       InterruptionModeAdaptive,
				InterruptionDetector:   &testInterruptionDetector{stream: &testInterruptionStream{events: make(chan OverlappingSpeechEvent)}},
				DisableUserAwayTimeout: true, DisableAgentSpeakingStallTimeout: true,
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := session.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := session.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A verdict must survive both playback ending and delayed provider delivery.
func TestAdaptiveBackchannelTranscriptDoesNotLeak(t *testing.T) {
	for _, delivery := range []string{"buffered", "late", "after-next-onset", "mixed-final", "next-bargein", "verdict-before-end", "late-interim", "late-preflight"} {
		t.Run(delivery, func(t *testing.T) {
			a := newAdaptiveTranscriptActivity(t)
			a.Session.Options.TurnDetection = TurnDetectionModeSTT
			a.Session.Options.MinEndpointingDelay = 60
			a.Session.Options.MaxEndpointingDelay = 60
			base := time.Now().Add(-10 * time.Second)
			a.armBackchannelBoundary(base)
			start := timeToUnixSeconds(base.Add(2 * time.Second))
			a.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &start})
			end := base.Add(2500 * time.Millisecond)
			if delivery != "verdict-before-end" {
				a.userSpeechStoppedAt = end
				a.OnEndOfSpeech(nil)
			}
			if !a.adaptiveOverlapPending.Load() {
				t.Fatal("test did not start an adaptive overlap")
			}
			event := timedGateEvent("Yeah.", timeToUnixSeconds(base.Add(4*time.Second)), floatPtr(timeToUnixSeconds(end)))
			event.SpeechStartTime = floatPtr(start)
			if delivery == "late-interim" {
				event.Type = stt.SpeechEventInterimTranscript
			}
			if delivery == "late-preflight" {
				event.Type = stt.SpeechEventPreflightTranscript
			}
			if delivery == "buffered" {
				a.OnFinalTranscript(event)
			}
			events := a.Session.UserInputTranscribedEvents()
			a.OnOverlapSpeechEnded(OverlappingSpeechEvent{OverlapStartedAt: &a.userSpeechStartedAt, DetectedAt: base.Add(3200 * time.Millisecond)})
			if delivery == "verdict-before-end" {
				a.userSpeechStoppedAt = end
				a.OnEndOfSpeech(nil)
			}
			if delivery != "next-bargein" {
				a.onAgentSpeechEnded(base.Add(3300 * time.Millisecond))
				a.Session.agentState = AgentStateListening
				a.flushHeldSTTEvents()
			}
			want := ""
			if delivery == "after-next-onset" || delivery == "mixed-final" || delivery == "next-bargein" {
				next := timeToUnixSeconds(base.Add(5 * time.Second))
				a.OnSTTStartOfSpeech(&stt.SpeechEvent{SpeechStartTime: &next})
				if delivery == "mixed-final" {
					event.Alternatives[0].Text = "Yeah. Stop."
					event.Alternatives[0].StartTime = 2
					event.Alternatives[0].EndTime = 5.5
					event.Alternatives[0].Words = []stt.TimedString{{Text: "Yeah.", StartTime: 2, EndTime: 2.5}, {Text: "Stop.", StartTime: 5, EndTime: 5.5}}
					event.SpeechEndTime = floatPtr(timeToUnixSeconds(base.Add(5500 * time.Millisecond)))
					want = "Stop."
				}
			}
			if delivery != "buffered" {
				if event.Type == stt.SpeechEventFinalTranscript {
					a.OnFinalTranscript(event)
				} else {
					a.OnInterimTranscript(event)
				}
			}
			if delivery == "next-bargein" {
				a.OnFinalTranscript(timedGateEvent("Stop.", timeToUnixSeconds(base.Add(6*time.Second)), nil))
				verdict := OverlappingSpeechEvent{IsInterruption: true, OverlapStartedAt: &a.userSpeechStartedAt, DetectedAt: base.Add(6 * time.Second)}
				a.OnOverlapSpeechEnded(verdict)
				a.OnInterruption(verdict)
				want = "Stop."
			}
			if got := a.pendingFinalEndOfTurnInfo().NewTranscript; got != want {
				t.Fatalf("accepted transcript = %q, want %q", got, want)
			}
			if len(a.Agent.ChatCtx.Items) != 0 {
				t.Fatal("backchannel added to conversation history")
			}
			select {
			case ev := <-events:
				if ev.Transcript != event.Alternatives[0].Text {
					t.Fatalf("display transcript = %q", ev.Transcript)
				}
			default:
				t.Fatal("transcript missing from display events")
			}
			text, err := a.CommitUserTurn(context.Background(), CommitUserTurnOptions{SkipReply: true, TranscriptTimeout: -1})
			if err != nil || text != want {
				t.Fatalf("commit = %q, %v, want %q", text, err, want)
			}
			items := a.Agent.ChatCtx.Items
			if want == "" && len(items) != 0 {
				t.Fatal("backchannel was committed to history")
			}
			if want != "" {
				if len(items) != 1 {
					t.Fatalf("history has %d items, want one user turn", len(items))
				}
				message, ok := items[0].(*llm.ChatMessage)
				if !ok || message.TextContent() != want {
					t.Fatalf("history contains backchannel: %#v", items)
				}
			}
			a.cancelPendingEOUDetection()
		})
	}
}
