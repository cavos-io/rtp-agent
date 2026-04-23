package agent

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestLLMTurnDetector(t *testing.T) {
	mock := &turnDetectorMockLLM{}
	detector := NewLLMTurnDetector(mock)
	
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "hello"}},
	})
	
	prob, err := detector.PredictEndOfTurn(context.Background(), chatCtx)
	if err != nil {
		t.Fatalf("PredictEndOfTurn failed: %v", err)
	}
	if prob != 0.9 {
		t.Errorf("Expected probability 0.9, got %v", prob)
	}

	// Test fallback
	mock.responses = []string{"invalid json"}
	prob, err = detector.PredictEndOfTurn(context.Background(), chatCtx)
	if err == nil {
		t.Error("Expected error for invalid JSON, got nil")
	}
	// Fallback should be 0.2 for "hello" (no punctuation)
	if prob != 0.2 {
		t.Errorf("Expected fallback probability 0.2, got %v", prob)
	}
}
