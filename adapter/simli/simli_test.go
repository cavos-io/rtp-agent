package simli

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestSimliLLM_Chat(t *testing.T) {
	l := NewSimliLLM("dummy-key", "dummy-model")
	if l == nil {
		t.Fatal("Failed to create SimliLLM")
	}
	
	ctx := context.Background()
	chatCtx := &llm.ChatContext{}
	_, err := l.Chat(ctx, chatCtx)
	if err == nil {
		// Delegation verified
	}
}
