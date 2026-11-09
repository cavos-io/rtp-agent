package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"github.com/cavos-io/rtp-agent/core/vad"
)

func newSpeechStartPipeline(mode TurnDetectionMode, fromSTT, voiceActivity bool) (*PipelineAgent, *AgentSession, *AgentActivity) {
	base := NewAgent("test")
	session := NewAgentSession(base, nil, AgentSessionOptions{TurnDetection: mode, SpeechStartFromSTT: fromSTT})
	activity := NewAgentActivity(base, session)
	session.activity = activity
	pipeline := NewPipelineAgent(nil, &fakePipelineSTT{voiceActivity: voiceActivity}, nil, nil, llm.NewChatContext())
	pipeline.session = session
	pipeline.ctx = context.Background()
	session.Assistant = pipeline
	return pipeline, session, activity
}

func vadEvents(events ...*vad.VADEvent) *fakePipelineVADStream {
	return &fakePipelineVADStream{events: events}
}

func sttStart(at time.Time) *stt.SpeechEvent {
	value := float64(at.UnixNano()) / 1e9
	return &stt.SpeechEvent{Type: stt.SpeechEventStartOfSpeech, SpeechStartTime: &value}
}

func sawUserSpeaking(events <-chan UserStateChangedEvent) bool {
	for {
		select {
		case ev := <-events:
			if ev.NewState == UserStateSpeaking {
				return true
			}
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}
}

func TestSileroStartAloneDoesNotStartSpeechWhenSTTDecides(t *testing.T) {
	pipeline, session, activity := newSpeechStartPipeline(TurnDetectionModeSTT, true, true)
	events := session.UserStateChangedEvents()

	pipeline.vadLoop(vadEvents(
		&vad.VADEvent{Type: vad.VADEventStartOfSpeech, SpeechDuration: 0.1},
		&vad.VADEvent{Type: vad.VADEventInferenceDone, SpeechDuration: 2, RawAccumulatedSpeech: 2},
		&vad.VADEvent{Type: vad.VADEventEndOfSpeech},
	))

	if sawUserSpeaking(events) || activity.isUserSpeaking() {
		t.Fatal("an unconfirmed Silero start must not put the user in speaking state")
	}
}

func TestSTTStartBorrowsRecentSileroStartTime(t *testing.T) {
	pipeline, session, activity := newSpeechStartPipeline(TurnDetectionModeSTT, true, true)
	pipeline.vadLoop(vadEvents(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, SpeechDuration: 0.3}))
	sttAt := time.Now()

	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{sttStart(sttAt)}})

	if !activity.isUserSpeaking() || session.UserStateValue() != UserStateSpeaking {
		t.Fatal("an STT start must put the user in speaking state")
	}
	activity.userTurnMu.Lock()
	started := activity.userSpeechStartedAt
	activity.userTurnMu.Unlock()
	if gap := sttAt.Sub(started); gap < 200*time.Millisecond || gap > 600*time.Millisecond {
		t.Fatalf("speech start is %v before the STT start, want Silero's earlier time (about 0.3 s)", gap)
	}
}

func TestSTTStartIgnoresStaleSileroStartTime(t *testing.T) {
	pipeline, _, activity := newSpeechStartPipeline(TurnDetectionModeSTT, true, true)
	pipeline.vadLoop(vadEvents(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, SpeechDuration: 3}))
	sttAt := time.Now()

	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{sttStart(sttAt)}})

	activity.userTurnMu.Lock()
	started := activity.userSpeechStartedAt
	activity.userTurnMu.Unlock()
	if gap := sttAt.Sub(started); gap < -50*time.Millisecond || gap > 50*time.Millisecond {
		t.Fatalf("speech start is %v before the STT start, want the STT's own time", gap)
	}
}

func TestSileroStartActsWhileSTTSpeechIsOpen(t *testing.T) {
	pipeline, _, activity := newSpeechStartPipeline(TurnDetectionModeSTT, true, true)
	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{sttStart(time.Now())}})
	pipeline.vadLoop(vadEvents(
		&vad.VADEvent{Type: vad.VADEventStartOfSpeech, SpeechDuration: 0.1},
		&vad.VADEvent{Type: vad.VADEventEndOfSpeech},
	))

	if activity.isUserSpeaking() {
		t.Fatal("Silero's end of a confirmed segment must end user speech as before")
	}
}

func TestSTTStartActsInVADTurnModeWhenSTTDecides(t *testing.T) {
	pipeline, _, activity := newSpeechStartPipeline(TurnDetectionModeVAD, true, true)

	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{sttStart(time.Now())}})

	if !activity.isUserSpeaking() {
		t.Fatal("the STT decides speech start regardless of the turn detection mode")
	}
}

func TestSTTSpeechBoundariesBypassHoldWhileAgentSpeaking(t *testing.T) {
	pipeline, session, activity := newSpeechStartPipeline(TurnDetectionModeSTT, true, true)
	session.agentState = AgentStateSpeaking
	activity.holdSTTWhileAgentSpeaking = true

	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{sttStart(time.Now())}})

	if !activity.isUserSpeaking() {
		t.Fatal("an STT start while the agent speaks must start user speech, not wait in the hold buffer")
	}

	pipeline.sttLoop(&fakePipelineRecognizeStream{events: []*stt.SpeechEvent{{Type: stt.SpeechEventEndOfSpeech}}})

	if activity.isUserSpeaking() {
		t.Fatal("an STT end while the agent speaks must end user speech")
	}
	if held := len(activity.heldSTTEvents); held != 0 {
		t.Fatalf("held STT events = %d, want speech boundaries never held when the STT decides", held)
	}
}

func TestSileroDecidesWithoutSTTVoiceActivity(t *testing.T) {
	for name, tc := range map[string]struct{ fromSTT, voiceActivity bool }{
		"option off":            {false, true},
		"no STT voice activity": {true, false},
	} {
		t.Run(name, func(t *testing.T) {
			pipeline, session, _ := newSpeechStartPipeline(TurnDetectionModeSTT, tc.fromSTT, tc.voiceActivity)
			events := session.UserStateChangedEvents()

			pipeline.vadLoop(vadEvents(&vad.VADEvent{Type: vad.VADEventStartOfSpeech, SpeechDuration: 0.1}))

			if !sawUserSpeaking(events) {
				t.Fatal("Silero must start speech when the STT does not decide")
			}
		})
	}
}
