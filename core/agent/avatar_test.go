package agent

import (
	"context"
	"testing"
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
	state        AvatarState
}

func (r *recordingAvatarProvider) Start(ctx context.Context) error {
	r.startCalls++
	r.startContext = ctx
	return nil
}

func (r *recordingAvatarProvider) UpdateState(state AvatarState) error {
	r.state = state
	return nil
}
