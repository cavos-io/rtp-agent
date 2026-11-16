package agent

import (
	"context"
	"errors"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
	"go.opentelemetry.io/otel/codes"
	"testing"
)

func TestShutdownPendingTurnTraceClassification(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "real-close-failure"}[fail], func(t *testing.T) {
			recorder := traceParityRecorder(t)
			a := &turnCompletedAgent{Agent: NewAgent("test"), turns: make(chan *llm.ChatMessage, 1)}
			a.TurnDetection = TurnDetectionModeManual
			a.LLM = &fakeGenerationLLM{}
			s := NewAgentSession(a, nil, AgentSessionOptions{SessionCloseTranscriptTimeout: .25})
			cause := errors.New("avatar close failed")
			if fail {
				a.Avatar = &fakeAvatarProvider{closeErr: cause}
			}
			s.activity = NewAgentActivity(a, s)
			s.started = true
			s.activity.OnFinalTranscript(&stt.SpeechEvent{Alternatives: []stt.SpeechData{{Text: "closing turn", Confidence: .9}}})
			err := s.Stop(context.Background())
			if fail {
				if !errors.Is(err, cause) {
					t.Fatalf("lost real error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			select {
			case msg := <-a.turns:
				if msg.TextContent() != "closing turn" {
					t.Fatal("lost final transcript")
				}
			default:
				t.Fatal("final-turn hook skipped")
			}
			found := map[string]bool{}
			for _, span := range recorder.Ended() {
				if span.Name() == "session_close" || span.Name() == "drain_agent_activity" {
					found[span.Name()] = true
					want := fail && span.Name() == "session_close"
					if (span.Status().Code == codes.Error) != want {
						t.Fatalf("%s status=%v", span.Name(), span.Status())
					}
				}
			}
			if len(found) != 2 {
				t.Fatalf("missing shutdown spans: %v", found)
			}
			if err := s.Stop(context.Background()); err != nil {
				t.Fatalf("repeat stop: %v", err)
			}
		})
	}
}

func TestShutdownCommitClassificationRetainsFailures(t *testing.T) {
	failure := errors.New("real failure")
	for _, tc := range []struct {
		err    error
		normal bool
	}{
		{ErrAgentSessionNotRunning, true},
		{errAgentSessionClosingReply, true},
		{errors.Join(ErrAgentSessionNotRunning, failure), false},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{failure, false},
	} {
		if got := expectedStoppedSessionCommit(tc.err); got != tc.normal {
			t.Fatalf("%v classified %v", tc.err, got)
		}
	}
}

func TestCloseSoonPendingTurnTraceIsNormal(t *testing.T) {
	recorder := traceParityRecorder(t)
	a := NewAgent("test")
	a.TurnDetection = TurnDetectionModeManual
	a.LLM = &fakeGenerationLLM{}
	s := NewAgentSession(a, nil, AgentSessionOptions{SessionCloseTranscriptTimeout: .25})
	s.activity = NewAgentActivity(a, s)
	s.started = true
	s.activity.OnFinalTranscript(&stt.SpeechEvent{Alternatives: []stt.SpeechData{{Text: "last turn"}}})
	s.CloseSoon(CloseReasonUserInitiated)
	found := 0
	for _, span := range recorder.Ended() {
		if span.Name() == "session_close" || span.Name() == "drain_agent_activity" {
			found++
			if span.Status().Code == codes.Error {
				t.Fatalf("unexpected error: %s %v", span.Name(), span.Status())
			}
		}
	}
	if found != 2 {
		t.Fatalf("shutdown span count=%d", found)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
