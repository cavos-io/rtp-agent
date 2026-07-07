package cavos

import (
	"testing"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestCavosInterruptionGateDecide(t *testing.T) {
	gate := NewInterruptionGate()

	tests := []struct {
		name             string
		agentSpeaking    bool
		speechMs         int
		transcript       string
		expectedDecision agent.InterruptionDecision
		expectedReason   string
	}{
		{
			name:             "speech too short",
			agentSpeaking:    true,
			speechMs:         200,
			transcript:       "oke",
			expectedDecision: agent.InterruptionIgnore,
			expectedReason:   "speech_too_short",
		},
		{
			name:             "backchannel suppressed",
			agentSpeaking:    true,
			speechMs:         600,
			transcript:       "iya",
			expectedDecision: agent.InterruptionIgnore,
			expectedReason:   "backchannel_suppressed",
		},
		{
			name:             "clear interrupt intent",
			agentSpeaking:    true,
			speechMs:         600,
			transcript:       "tunggu dulu",
			expectedDecision: agent.InterruptionInterruptAgent,
			expectedReason:   "clear_interrupt_intent",
		},
		{
			name:             "strong barge-in with empty transcript",
			agentSpeaking:    true,
			speechMs:         1200,
			transcript:       "",
			expectedDecision: agent.InterruptionInterruptAgent,
			expectedReason:   "strong_barge_in",
		},
		{
			name:             "strong barge-in with transcript",
			agentSpeaking:    true,
			speechMs:         1200,
			transcript:       "halo selamat pagi",
			expectedDecision: agent.InterruptionInterruptAgent,
			expectedReason:   "strong_barge_in",
		},
		{
			name:             "long non-backchannel",
			agentSpeaking:    true,
			speechMs:         800,
			transcript:       "saya ingin bertanya tentang paket",
			expectedDecision: agent.InterruptionInterruptAgent,
			expectedReason:   "long_non_backchannel",
		},
		{
			name:             "needs more speech",
			agentSpeaking:    true,
			speechMs:         800,
			transcript:       "",
			expectedDecision: agent.InterruptionContinueListening,
			expectedReason:   "needs_more_speech",
		},
		{
			name:             "agent not speaking",
			agentSpeaking:    false,
			speechMs:         1000,
			transcript:       "oke",
			expectedDecision: agent.InterruptionAcceptUserTurn,
			expectedReason:   "agent_not_speaking",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := gate.Decide(tt.agentSpeaking, tt.speechMs, tt.transcript)
			if result.Decision != tt.expectedDecision {
				t.Errorf("decision = %d, want %d", result.Decision, tt.expectedDecision)
			}
			if result.Reason != tt.expectedReason {
				t.Errorf("reason = %q, want %q", result.Reason, tt.expectedReason)
			}
		})
	}
}
