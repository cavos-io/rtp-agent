package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
)

type blockingStartAssistant struct {
	started chan struct{}
	release chan struct{}
}

func TestStopDuringStartHonorsContext(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- s.Stop(ctx) }()
	select {
	case err := <-stopDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stop error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop ignored context cancellation while Start was in progress")
	}

	close(assistant.release)
	if err := <-startDone; err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("final Stop: %v", err)
	}
}

func TestConcurrentStartHonorsContext(t *testing.T) {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	s := NewAgentSession(agent, nil, AgentSessionOptions{})
	assistant := &blockingStartAssistant{started: make(chan struct{}), release: make(chan struct{})}
	s.Assistant = assistant

	firstDone := make(chan error, 1)
	go func() { firstDone <- s.Start(context.Background()) }()
	<-assistant.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	secondDone := make(chan error, 1)
	go func() { secondDone <- s.Start(ctx) }()
	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second Start error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent Start ignored context cancellation")
	}

	close(assistant.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

type teardownFailingStartAssistant struct{}

func (*teardownFailingStartAssistant) Start(context.Context, *AgentSession) error {
	return errors.New("start failed")
}

func (*teardownFailingStartAssistant) OnAudioFrame(context.Context, *model.AudioFrame) {}

func (*teardownFailingStartAssistant) SetPublishAudio(func(context.Context, *model.AudioFrame) error) {
}

func TestFailedStartClosesRunTeardown(t *testing.T) {
	agent := NewAgent("test")
	agent.TTS = &fakePipelineTTS{}
	agent.LLM = &fakeGenerationLLM{}
	agent.STT = &fakePipelineSTT{}
	agent.VAD = &fakePipelineVAD{}
	s := NewAgentSession(agent, nil, AgentSessionOptions{})
	s.Assistant = &teardownFailingStartAssistant{}

	if err := s.Start(context.Background()); err == nil {
		t.Fatal("Start error = nil, want failure")
	}
	s.mu.Lock()
	teardown := s.teardownCh
	s.mu.Unlock()
	select {
	case <-teardown:
	case <-time.After(time.Second):
		t.Fatal("failed Start left its teardown generation open")
	}
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
