package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
)

func TestGetDtmfTask(t *testing.T) {
	task := NewGetDtmfTask(4, false)
	
	// Simulate DTMF inputs
	task.onSipDTMFReceived("1")
	task.onSipDTMFReceived("2")
	task.onSipDTMFReceived("3")
	task.onSipDTMFReceived("4")
	
	// Manually trigger reply (normally triggered by timer or stop event)
	task.generateDtmfReply()
	
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	resRaw, err := task.WaitAny(ctx)
	if err != nil {
		t.Fatalf("Task failed: %v", err)
	}
	res := resRaw.(*GetDtmfResult)
	if res.UserInput != "1 2 3 4" {
		t.Errorf("Expected 1 2 3 4, got %s", res.UserInput)
	}
}

func TestGetDtmfTask_Confirmation(t *testing.T) {
	task := NewGetDtmfTask(4, true)
	
	tool := &confirmInputsTool{task: task}
	_, err := tool.Execute(context.Background(), map[string]any{
		"inputs": []interface{}{"5", "6", "7", "8"},
	})
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}
	
	resRaw, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("Task failed: %v", err)
	}
	res := resRaw.(*GetDtmfResult)
	if res.UserInput != "5 6 7 8" {
		t.Errorf("Expected 5 6 7 8, got %s", res.UserInput)
	}
}

func TestTaskGroup_Basic(t *testing.T) {
	t1 := NewGetDtmfTask(1, false)
	
	group := NewTaskGroup(false, false)
	group.Add("t1", "t1", func() agent.AgentInterface { return t1 })
	
	if len(group.RegisteredTasks) != 1 {
		t.Errorf("Expected 1 task, got %d", len(group.RegisteredTasks))
	}
}

func TestEmailAddressTask(t *testing.T) {
	task := NewGetEmailTask(false)
	
	// Test the tool
	tool := &updateEmailTool{task: task}
	_, err := tool.Execute(context.Background(), map[string]any{
		"email": "test@example.com",
	})
	if err != nil {
		t.Fatalf("Tool failed: %v", err)
	}
	
	resRaw, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("Task failed: %v", err)
	}
	res := resRaw.(*GetEmailResult)
	if res.Email != "test@example.com" {
		t.Errorf("Expected test@example.com, got %s", res.Email)
	}
}
