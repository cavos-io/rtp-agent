package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
)

type finalizingTestSTTStream struct {
	fakePipelineRecognizeStream
	requested chan struct{}
	err       error
}

func (s *finalizingTestSTTStream) Finalize() error {
	s.requested <- struct{}{}
	return s.err
}

func TestVADTurnWaitsForFinalizedSTT(t *testing.T) {
	for _, response := range []string{"final", "timeout"} {
		t.Run(response, func(t *testing.T) {
			agent := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 4)}
			agent.TurnDetection = TurnDetectionModeVAD
			agent.VAD, agent.STT = &fakePipelineVAD{}, &fakePipelineSTT{}
			session := NewAgentSession(agent, nil, AgentSessionOptions{MinEndpointingDelay: 0.01, MaxEndpointingDelay: 0.01, DisableUserAwayTimeout: true})
			pipeline := NewPipelineAgent(agent.VAD, agent.STT, nil, nil, agent.ChatCtx)
			pipeline.session = session
			stream := &finalizingTestSTTStream{requested: make(chan struct{}, 4)}
			pipeline.sttStream = stream
			session.Assistant = pipeline
			activity := NewAgentActivity(agent, session)
			session.activity = activity
			defer activity.Stop()
			activity.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
			activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "ya bentar", Confidence: 1}}})
			started := time.Now()
			activity.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
			select {
			case message := <-agent.turns:
				t.Fatalf("committed before finalization: %q", message.TextContent())
			case <-time.After(40 * time.Millisecond):
			}
			select {
			case <-stream.requested:
			default:
				t.Fatal("VAD end did not request STT finalization")
			}
			want := "ya bentar"
			if response == "final" {
				activity.OnFinalTranscript(&stt.SpeechEvent{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "Ambi", Confidence: 1}}})
				select {
				case message := <-agent.turns:
					t.Fatalf("partial final released the turn: %q", message.TextContent())
				case <-time.After(40 * time.Millisecond):
				}
				pipeline.ctx = context.Background()
				pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{
					{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "itu lulusan apa ya", Confidence: 1}}},
					{Type: stt.SpeechEventEndOfSpeech, SpeechEndTime: floatPtr(timeToUnixSeconds(time.Now()))},
				}})
				want += " Ambi itu lulusan apa ya"
			}
			select {
			case message := <-agent.turns:
				if message.TextContent() != want {
					t.Fatalf("committed %q, want %q", message.TextContent(), want)
				}
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("finalization wait never committed")
			}
			if response == "final" && time.Since(started) > 500*time.Millisecond {
				t.Fatal("completed STT did not release the finalization wait promptly")
			}
			if response == "timeout" && time.Since(started) < 900*time.Millisecond {
				t.Fatal("committed before the finalization cap")
			}
			select {
			case message := <-agent.turns:
				t.Fatalf("second turn generated: %q", message.TextContent())
			case <-time.After(30 * time.Millisecond):
			}
		})
	}
}

func TestAdaptiveFinalizationWaitsForBargeInVerdict(t *testing.T) {
	for _, interruption := range []bool{false, true} {
		t.Run(map[bool]string{false: "backchannel", true: "barge-in"}[interruption], func(t *testing.T) {
			a := newAdaptiveTranscriptActivity(t)
			pipeline := NewPipelineAgent(a.Agent.VAD, a.Agent.STT, nil, nil, a.Agent.ChatCtx)
			stream := &finalizingTestSTTStream{requested: make(chan struct{}, 4)}
			pipeline.sttStream = stream
			a.Session.Assistant = pipeline
			a.armBackchannelBoundary(time.Now().Add(-2 * time.Second))
			a.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
			a.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
			select {
			case <-stream.requested:
				t.Fatal("pending verdict finalized STT")
			default:
			}
			verdict := OverlappingSpeechEvent{OverlapStartedAt: &a.userSpeechStartedAt, DetectedAt: time.Now(), IsInterruption: interruption}
			a.OnOverlapSpeechEnded(verdict)
			if interruption {
				a.OnInterruption(verdict)
				select {
				case <-stream.requested:
				case <-time.After(time.Second):
					t.Fatal("late barge-in verdict did not finalize STT")
				}
			}
			select {
			case <-stream.requested:
				t.Fatal("backchannel finalized STT or barge-in finalized twice")
			default:
			}
			a.cancelPendingEOUDetection()
		})
	}
}

func TestSTTFinalizationIgnoresStaleBoundaryAndNewOnset(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.agentState = AgentStateListening
	pipeline := NewPipelineAgent(a.Agent.VAD, a.Agent.STT, nil, nil, a.Agent.ChatCtx)
	stream := &finalizingTestSTTStream{requested: make(chan struct{}, 4)}
	pipeline.sttStream = stream
	a.Session.Assistant = pipeline
	a.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	a.OnEndOfSpeech(&vad.VADEvent{Type: vad.VADEventEndOfSpeech})
	a.finishSTTFinalization(&stt.SpeechEvent{}) // Untimed native endpointing is not a finalize acknowledgement.
	a.finishSTTFinalization(&stt.SpeechEvent{SpeechEndTime: floatPtr(timeToUnixSeconds(a.userSpeechStartedAt.Add(-time.Second)))})
	a.userTurnMu.Lock()
	waiting := !a.sttFinalizeDeadline.IsZero()
	a.userTurnMu.Unlock()
	if !waiting {
		t.Fatal("stale provider boundary released current turn")
	}
	a.OnStartOfSpeech(&vad.VADEvent{Type: vad.VADEventStartOfSpeech})
	a.userTurnMu.Lock()
	waiting = !a.sttFinalizeDeadline.IsZero()
	a.userTurnMu.Unlock()
	if waiting {
		t.Fatal("new speech inherited previous finalization deadline")
	}
	a.cancelPendingEOUDetection()
}

func TestSTTFinalizationErrorDoesNotHoldTurn(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.agentState = AgentStateListening
	pipeline := NewPipelineAgent(a.Agent.VAD, a.Agent.STT, nil, nil, a.Agent.ChatCtx)
	stream := &finalizingTestSTTStream{requested: make(chan struct{}, 4), err: errors.New("write failed")}
	pipeline.sttStream = stream
	a.Session.Assistant = pipeline
	a.requestSTTFinalization()
	a.userTurnMu.Lock()
	waiting := !a.sttFinalizeDeadline.IsZero()
	a.userTurnMu.Unlock()
	if waiting {
		t.Fatal("failed finalization left the turn waiting")
	}
}

func TestHeldFinalizationBoundaryFollowsHeldTranscripts(t *testing.T) {
	a := newAdaptiveTranscriptActivity(t)
	a.Session.Options.MinEndpointingDelay = 60
	a.Session.Options.MaxEndpointingDelay = 60
	pipeline := NewPipelineAgent(a.Agent.VAD, a.Agent.STT, nil, nil, a.Agent.ChatCtx)
	pipeline.session, pipeline.ctx = a.Session, context.Background()
	a.Session.Assistant = pipeline
	a.holdSTTWhileAgentSpeaking = true
	a.userSpeechStoppedAt = time.Now()
	a.sttFinalizeRequestedAt = time.Now()
	a.sttFinalizeDeadline = time.Now().Add(time.Second)
	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{
		{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "first", Confidence: 1}}},
		{Type: stt.SpeechEventFinalTranscript, Alternatives: []stt.SpeechData{{Text: "tail", Confidence: 1}}},
		{Type: stt.SpeechEventEndOfSpeech, SpeechEndTime: floatPtr(timeToUnixSeconds(time.Now()))},
	}})
	if a.sttFinalizeDeadline.IsZero() {
		t.Fatal("completion released the turn before held finals were accepted")
	}
	a.releaseAdaptiveTranscripts(time.Time{}, time.Time{})
	if !a.sttFinalizeDeadline.IsZero() || a.pendingFinalEndOfTurnInfo().NewTranscript != "first tail" {
		t.Fatal("held finals and completion were not replayed in order")
	}
	a.cancelPendingEOUDetection()
}
