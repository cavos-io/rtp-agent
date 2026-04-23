package ivr

import (
	"context"
	"testing"
	"time"
)

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		s1, s2   string
		expected float64
	}{
		{"hello world", "hello world", 1.0},
		{"hello world", "hello", 0.5},
		{"hello world", "bye world", 1.0 / 3.0},
		{"", "", 1.0},
		{"a b c", "d e f", 0.0},
	}

	for _, tt := range tests {
		res := jaccardSimilarity(tt.s1, tt.s2)
		if (res - tt.expected) > 0.001 || (tt.expected - res) > 0.001 {
			t.Errorf("jaccardSimilarity(%q, %q) = %v, expected %v", tt.s1, tt.s2, res, tt.expected)
		}
	}
}

func TestLoopDetector(t *testing.T) {
	ld := NewLoopDetector()
	ld.consecutiveThreshold = 2
	
	ld.AddChunk("hello")
	ld.AddChunk("hello")
	if ld.CheckLoopDetection() {
		t.Error("Unexpected loop detected (count should be 1)")
	}
	
	ld.AddChunk("hello")
	if !ld.CheckLoopDetection() {
		t.Error("Loop NOT detected (count should be 2)")
	}
	
	ld.Reset()
	if ld.CheckLoopDetection() {
		t.Error("Unexpected loop detected after reset")
	}
}

type mockSession struct {
	replyCalled bool
}

func (m *mockSession) GenerateReply(ctx context.Context, userInput string, allowInterruptions bool) (any, error) {
	m.replyCalled = true
	return nil, nil
}

func (m *mockSession) GetPublisher() interface {
	Identity() string
	PublishData(data []byte, topic string, destinationSIDs []string) error
} {
	return nil
}

func TestIVRActivity_Silence(t *testing.T) {
	session := &mockSession{}
	ivr := NewIVRActivity(session)
	ivr.maxSilenceDuration = 100 * time.Millisecond
	
	ivr.OnUserStateChanged(UserStateListening, UserStateListening)
	ivr.OnAgentStateChanged(AgentStateIdle, AgentStateIdle)
	
	time.Sleep(200 * time.Millisecond)
	
	if !session.replyCalled {
		t.Error("Expected GenerateReply to be called due to silence")
	}
}

func TestIVRActivity_NoSilenceWhenSpeaking(t *testing.T) {
	session := &mockSession{}
	ivr := NewIVRActivity(session)
	ivr.maxSilenceDuration = 100 * time.Millisecond
	
	ivr.OnUserStateChanged(UserStateListening, UserStateSpeaking)
	ivr.OnAgentStateChanged(AgentStateIdle, AgentStateIdle)
	
	time.Sleep(200 * time.Millisecond)
	
	if session.replyCalled {
		t.Error("GenerateReply should NOT be called when user is speaking")
	}
}
