package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/model"
)

func TestMultimodalAgent(t *testing.T) {
	eventCh := make(chan llm.RealtimeEvent, 1)
	rtSession := &mockRealtimeSession{eventCh: eventCh}
	modelRealtime := &mockRealtimeModel{session: rtSession}
	
	agent := NewMultimodalAgent(modelRealtime, nil)
	
	session := &AgentSession{
		Tools: []interface{}{},
	}
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := agent.Start(ctx, session)
	if err != nil {
		t.Fatalf("Failed to start multimodal agent: %v", err)
	}

	// Test audio event
	eventCh <- llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeAudio,
		Data: []byte{0, 0, 0, 0},
	}

	// Test speech started event
	eventCh <- llm.RealtimeEvent{
		Type: llm.RealtimeEventTypeSpeechStarted,
	}

	// Give it a moment to process
	time.Sleep(50 * time.Millisecond)
	
	if session.UserState != UserStateSpeaking {
		t.Errorf("Expected user state Speaking, got %v", session.UserState)
	}

	agent.OnAudioFrame(ctx, &model.AudioFrame{Data: []byte("user audio")})
}
