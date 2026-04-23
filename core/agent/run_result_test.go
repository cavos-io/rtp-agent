package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestRunResult(t *testing.T) {
	chatCtx := llm.NewChatContext()
	res := NewRunResult[string](chatCtx)
	
	// Test AddEvent
	ev := &ChatMessageRunEvent{
		Item: &llm.ChatMessage{
			Role:      llm.ChatRoleUser,
			Content:   []llm.ChatContent{{Text: "hi"}},
			CreatedAt: time.Now(),
		},
	}
	res.AddEvent(ev)
	if len(res.GetEvents()) != 1 {
		t.Errorf("Expected 1 event, got %d", len(res.GetEvents()))
	}
	
	// Test WatchTask
	done := make(chan struct{})
	res.WatchTask(done)
	
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	err := res.Wait(ctx)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
	
	close(done)
	err = res.Wait(context.Background())
	if err != nil {
		t.Errorf("Wait failed after task done: %v", err)
	}
	
	if !res.done {
		t.Error("Expected res.done to be true")
	}
}

func TestRunAssert(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "hello there"}},
	})
	chatCtx.Append(&llm.FunctionCall{
		Name: "test_func",
	})
	
	assert := &RunAssert{ChatCtx: chatCtx}
	
	err := assert.ContainsMessage(llm.ChatRoleAssistant, "hello").
		IsFunctionCall("test_func").
		HasError()
	if err != nil {
		t.Errorf("Assertions failed: %v", err)
	}
	
	err = assert.ContainsMessage(llm.ChatRoleUser, "missing").HasError()
	if err == nil {
		t.Error("Expected error for missing message, got nil")
	}
}
