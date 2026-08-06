package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type recordingOrderAssistant struct {
	mu    sync.Mutex
	order *[]string
}

func (*recordingOrderAssistant) Start(context.Context, *AgentSession) error { return nil }

func (*recordingOrderAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {}

func (*recordingOrderAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {}

func (a *recordingOrderAssistant) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	*a.order = append(*a.order, "assistant")
	return nil
}

func newRecorderTestSession() *AgentSession {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	return NewAgentSession(agent, nil, AgentSessionOptions{})
}

func TestAgentSessionStopFinalizesRecorder(t *testing.T) {
	s := newRecorderTestSession()
	s.Assistant = &recordingOrderAssistant{order: new([]string)}

	stops := 0
	s.SetRecorderStopper(func() error {
		stops++
		return nil
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if stops != 0 {
		t.Fatalf("recorder stopped before session teardown: stops = %d", stops)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stops != 1 {
		t.Fatalf("recorder stops = %d, want 1", stops)
	}
}

func TestAgentSessionStopFinalizesRecorderAfterAssistantTeardown(t *testing.T) {
	order := new([]string)
	s := newRecorderTestSession()
	s.Assistant = &recordingOrderAssistant{order: order}
	s.SetRecorderStopper(func() error {
		*order = append(*order, "recorder")
		return nil
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// The recording must close last: audio keeps arriving while the final user
	// turn is committed during teardown.
	want := []string{"assistant", "recorder"}
	if len(*order) != len(want) {
		t.Fatalf("teardown order = %v, want %v", *order, want)
	}
	for i, step := range want {
		if (*order)[i] != step {
			t.Fatalf("teardown order = %v, want %v", *order, want)
		}
	}
}

func TestAgentSessionStopFinalizesRecorderWhenNeverStarted(t *testing.T) {
	// StartSession starts the recorder before AgentSession.Start, so a session
	// that never started can still own an open recording.
	s := newRecorderTestSession()

	stops := 0
	s.SetRecorderStopper(func() error {
		stops++
		return nil
	})

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stops != 1 {
		t.Fatalf("recorder stops = %d, want 1", stops)
	}
}

func TestAgentSessionStopRunsRecorderStopperOnce(t *testing.T) {
	s := newRecorderTestSession()
	s.Assistant = &recordingOrderAssistant{order: new([]string)}

	stops := 0
	s.SetRecorderStopper(func() error {
		stops++
		return nil
	})

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if stops != 1 {
		t.Fatalf("recorder stops = %d, want 1", stops)
	}
}

func TestAgentSessionStopReturnsRecorderError(t *testing.T) {
	s := newRecorderTestSession()
	s.Assistant = &recordingOrderAssistant{order: new([]string)}

	recorderErr := errors.New("close recording writer")
	s.SetRecorderStopper(func() error { return recorderErr })

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); !errors.Is(err, recorderErr) {
		t.Fatalf("Stop error = %v, want %v", err, recorderErr)
	}
}

func TestAgentSessionStopWithoutRecorderStopper(t *testing.T) {
	s := newRecorderTestSession()
	s.Assistant = &recordingOrderAssistant{order: new([]string)}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
