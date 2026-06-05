package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/telemetry"
)

type avatarContextKey string

func TestAgentSessionStartPassesContextToAvatarProvider(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &recordingAvatarProvider{}
	baseAgent.Avatar = avatar

	ctx := context.WithValue(context.Background(), avatarContextKey("request_id"), "session-a")
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("Start error = %v", err)
	}

	if avatar.startCalls != 1 {
		t.Fatalf("avatar startCalls = %d, want 1", avatar.startCalls)
	}
	if got := avatar.startContext.Value(avatarContextKey("request_id")); got != "session-a" {
		t.Fatalf("avatar start context request_id = %#v, want session-a", got)
	}
}

func TestAgentSessionStartDoesNotRestartAvatarProvider(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &recordingAvatarProvider{}
	baseAgent.Avatar = avatar

	ctx := context.Background()
	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})

	if err := session.Start(ctx); err != nil {
		t.Fatalf("first Start error = %v", err)
	}
	if err := session.Start(ctx); err != nil {
		t.Fatalf("second Start error = %v", err)
	}

	if avatar.startCalls != 1 {
		t.Fatalf("avatar startCalls = %d, want idempotent start once", avatar.startCalls)
	}
}

func TestAgentSessionStartSubscribesAvatarMetrics(t *testing.T) {
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &recordingAvatarProvider{}
	baseAgent.Avatar = avatar

	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	if err := session.Start(context.Background()); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	metrics := &telemetry.AvatarMetrics{PlaybackLatency: 0.125}

	avatar.emitMetrics(metrics)

	select {
	case ev := <-session.MetricsCollectedEvents():
		if ev.Metrics != metrics {
			t.Fatalf("MetricsCollectedEvent metrics = %#v, want original avatar metrics", ev.Metrics)
		}
	case <-time.After(time.Second):
		t.Fatal("MetricsCollectedEvents did not receive avatar metrics")
	}
}

func TestAgentSessionStartUnsubscribesAvatarMetricsOnStartError(t *testing.T) {
	errAvatar := errors.New("avatar start failed")
	baseAgent := NewAgent("test")
	baseAgent.VAD = &fakePipelineVAD{}
	baseAgent.STT = &fakePipelineSTT{}
	baseAgent.LLM = &fakeGenerationLLM{stream: &fakeGenerationLLMStream{}}
	baseAgent.TTS = &fakePipelineTTS{}
	avatar := &recordingAvatarProvider{startErr: errAvatar}
	baseAgent.Avatar = avatar

	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	err := session.Start(context.Background())
	if !errors.Is(err, errAvatar) {
		t.Fatalf("Start error = %v, want %v", err, errAvatar)
	}

	avatar.emitMetrics(&telemetry.AvatarMetrics{PlaybackLatency: 0.25})

	select {
	case ev := <-session.MetricsCollectedEvents():
		t.Fatalf("MetricsCollectedEvents received avatar metrics after failed Start: %#v", ev.Metrics)
	default:
	}
}

func TestAgentSessionStartUnsubscribesAvatarMetricsOnAssistantStartError(t *testing.T) {
	errAssistant := errors.New("assistant start failed")
	baseAgent := NewAgent("test")
	avatar := &recordingAvatarProvider{}
	baseAgent.Avatar = avatar

	session := NewAgentSession(baseAgent, nil, AgentSessionOptions{})
	session.Assistant = &failingStartAssistant{err: errAssistant}
	err := session.Start(context.Background())
	if !errors.Is(err, errAssistant) {
		t.Fatalf("Start error = %v, want %v", err, errAssistant)
	}

	avatar.emitMetrics(&telemetry.AvatarMetrics{PlaybackLatency: 0.5})

	select {
	case ev := <-session.MetricsCollectedEvents():
		t.Fatalf("MetricsCollectedEvents received avatar metrics after assistant Start failed: %#v", ev.Metrics)
	default:
	}
}

func TestAvatarProviderUpdateStateRecordsLatestState(t *testing.T) {
	avatar := &recordingAvatarProvider{}

	if err := avatar.UpdateState(AvatarStateSpeaking); err != nil {
		t.Fatalf("UpdateState error = %v", err)
	}
	if avatar.state != AvatarStateSpeaking {
		t.Fatalf("avatar state = %q, want speaking", avatar.state)
	}
	if err := avatar.UpdateState(AvatarStateIdle); err != nil {
		t.Fatalf("UpdateState idle error = %v", err)
	}
	if avatar.state != AvatarStateIdle {
		t.Fatalf("avatar state = %q, want idle", avatar.state)
	}
}

type recordingAvatarProvider struct {
	startCalls   int
	startContext context.Context
	startErr     error
	state        AvatarState
	metrics      AvatarMetricsHandler
}

func (r *recordingAvatarProvider) Start(ctx context.Context) error {
	r.startCalls++
	r.startContext = ctx
	return r.startErr
}

func (r *recordingAvatarProvider) UpdateState(state AvatarState) error {
	r.state = state
	return nil
}

func (r *recordingAvatarProvider) OnMetricsCollected(handler AvatarMetricsHandler) func() {
	r.metrics = handler
	return func() {
		r.metrics = nil
	}
}

func (r *recordingAvatarProvider) emitMetrics(metrics *telemetry.AvatarMetrics) {
	if r.metrics != nil {
		r.metrics(metrics)
	}
}

type failingStartAssistant struct {
	fakeSessionAssistant
	err error
}

func (f failingStartAssistant) Start(context.Context, *AgentSession) error {
	return f.err
}
