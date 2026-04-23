package agent

import (
	"context"
	"errors"
	"testing"
)

func TestNewAgent(t *testing.T) {
	instructions := "Be helpful"
	a := NewAgent(instructions)
	if a.Instructions != instructions {
		t.Errorf("Expected %q, got %q", instructions, a.Instructions)
	}
	if a.ChatCtx == nil {
		t.Error("Expected ChatCtx to be initialized")
	}
}

func TestAgentTask(t *testing.T) {
	task := NewAgentTask[string]("Do something")
	
	go func() {
		task.Complete("success")
	}()

	res, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny failed: %v", err)
	}
	if res.(string) != "success" {
		t.Errorf("Expected success, got %v", res)
	}

	task2 := NewAgentTask[int]("Fail")
	go func() {
		task2.Fail(errors.New("kaboom"))
	}()
	_, err = task2.WaitAny(context.Background())
	if err == nil || err.Error() != "kaboom" {
		t.Errorf("Expected kaboom error, got %v", err)
	}
}

func TestAgentTask_ContextCancel(t *testing.T) {
	task := NewAgentTask[string]("Wait")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	
	_, err := task.WaitAny(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}
