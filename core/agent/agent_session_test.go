package agent

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestAgentSession_Basic(t *testing.T) {
	a := NewAgent("test instructions")
	opts := AgentSessionOptions{
		AllowInterruptions: true,
	}
	s := NewAgentSession(a, nil, opts)
	
	if s.Agent != a {
		t.Error("Agent not set correctly")
	}
	if !s.Options.AllowInterruptions {
		t.Error("Options not set correctly")
	}
	
	s.UpdateAgentState(AgentStateSpeaking)
	if s.AgentState != AgentStateSpeaking {
		t.Errorf("Expected Speaking state, got %v", s.AgentState)
	}
	
	s.UpdateUserState(UserStateSpeaking)
	if s.UserState != UserStateSpeaking {
		t.Errorf("Expected UserSpeaking state, got %v", s.UserState)
	}
}

func TestAgentSession_Lifecycle(t *testing.T) {
	a := NewAgent("test instructions")
	s := NewAgentSession(a, nil, AgentSessionOptions{})
	s.VAD = &testMockVAD{}
	s.STT = &testMockSTT{}
	
	// Mock publisher to avoid nil panics in some flows
	s.Output.Publisher = &mockPublisher{}
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	
	if !s.started {
		t.Error("Session should be started")
	}
	
	err = s.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	
	if s.started {
		t.Error("Session should be stopped")
	}
}

func TestAgentSession_Metrics(t *testing.T) {
	a := NewAgent("test instructions")
	s := NewAgentSession(a, nil, AgentSessionOptions{})
	s.MetricsCollector = telemetry.NewUsageCollector()
	
	// Just test that we can collect and append
	s.MetricsCollector.Collect(&telemetry.LLMMetrics{PromptTokens: 10})
	
	// Simulate what reportUsageLoop does
	summary := s.MetricsCollector.GetSummary()
	s.ChatCtx.Append(&llm.MetricsReport{Usage: summary})
	
	if len(s.ChatCtx.Items) == 0 {
		t.Error("Metrics report not appended to ChatCtx")
	}
}

func TestAgentSession_UpdateAgent(t *testing.T) {
	a1 := NewAgent("instructions 1")
	s := NewAgentSession(a1, nil, AgentSessionOptions{})
	s.Output.Publisher = &mockPublisher{}
	s.VAD = &testMockVAD{}
	s.STT = &testMockSTT{}
	
	_ = s.Start(context.Background())
	
	a2 := NewAgent("instructions 2")
	err := s.UpdateAgent(a2, nil)
	if err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}
	
	if s.Agent != a2 {
		t.Error("Agent not updated")
	}
}
