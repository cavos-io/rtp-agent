package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type blockingStartAssistant struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingStartAssistant) Start(context.Context, *AgentSession) error {
	close(b.started)
	<-b.release
	return nil
}

func (b *blockingStartAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {}

func (b *blockingStartAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {}

func TestStopDuringStartWaitsThenTearsDown(t *testing.T) {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	s := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &blockingStartAssistant{started: make(chan struct{}), release: make(chan struct{})}
	s.Assistant = assistant

	startDone := make(chan error, 1)
	go func() { startDone <- s.Start(context.Background()) }()
	<-assistant.started

	stopDone := make(chan error, 1)
	go func() { stopDone <- s.Stop(context.Background()) }()

	select {
	case <-stopDone:
		t.Fatal("Stop returned during the starting window instead of waiting for start to finish")
	case <-time.After(50 * time.Millisecond):
	}

	close(assistant.release)

	if err := <-startDone; err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not complete after start finished")
	}

	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		t.Fatal("session still started after Stop raced with Start (Stop should win)")
	}
}
