package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestWarmTransferTask(t *testing.T) {
	chatCtx := llm.NewChatContext()
	chatCtx.Append(&llm.ChatMessage{Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "I need help with my billing"}}})
	
	task := NewWarmTransferTask("+123456789", "trunk-1", chatCtx, "Be polite.")
	
	if task.TargetPhoneNumber != "+123456789" {
		t.Errorf("Expected phone +123456789, got %s", task.TargetPhoneNumber)
	}
	
	// Test Tool Execution: connect_to_caller
	tool := task.Agent.Tools[0].(*connectToCallerTool)
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("ConnectToCaller tool failed: %v", err)
	}
	if res != "Connected to caller." {
		t.Errorf("Expected 'Connected to caller.', got %v", res)
	}
	
	// Check if task is completed
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = task.WaitAny(ctx)
	if err != nil {
		t.Errorf("WaitAny failed: %v", err)
	}
}
