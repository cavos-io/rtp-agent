package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/core/llm"
)

type agentTestTool struct {
	id   string
	name string
}

func (t *agentTestTool) ID() string {
	if t.id != "" {
		return t.id
	}
	return t.name
}

func (t *agentTestTool) Name() string { return t.name }

func (t *agentTestTool) Description() string { return "" }

func (t *agentTestTool) Parameters() map[string]any { return nil }

func (t *agentTestTool) Execute(context.Context, string) (string, error) { return "", nil }

func TestAgentUpdateChatContextFiltersFunctionItemsToAgentTools(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{&agentTestTool{name: "lookup"}}
	source := llm.NewChatContext()
	source.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})
	source.Append(&llm.FunctionCallOutput{ID: "lookup-output", Name: "lookup"})
	source.Append(&llm.FunctionCall{ID: "calendar-call", Name: "calendar"})
	source.Append(&llm.FunctionCallOutput{ID: "calendar-output", Name: "calendar"})

	if err := agent.UpdateChatContext(context.Background(), source); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want nil", err)
	}

	got := chatItemIDs(agent.ChatCtx.Items)
	want := []string{"msg_1", "lookup-call", "lookup-output"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("agent ChatCtx item IDs = %q, want %q", got, want)
	}
	if agent.ChatCtx == source {
		t.Fatal("UpdateChatContext reused source context, want copied context")
	}
}

func TestAgentUpdateChatContextCanKeepInvalidFunctionItems(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{&agentTestTool{name: "lookup"}}
	source := llm.NewChatContext()
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})
	source.Append(&llm.FunctionCall{ID: "calendar-call", Name: "calendar"})

	if err := agent.UpdateChatContext(context.Background(), source, false); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want nil", err)
	}

	got := chatItemIDs(agent.ChatCtx.Items)
	want := []string{"lookup-call", "calendar-call"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("agent ChatCtx item IDs = %q, want %q", got, want)
	}
}

func TestAgentUpdateToolsFiltersChatContextFunctionItems(t *testing.T) {
	agent := NewAgent("help")
	agent.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	agent.ChatCtx.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})
	agent.ChatCtx.Append(&llm.FunctionCallOutput{ID: "lookup-output", Name: "lookup"})
	agent.ChatCtx.Append(&llm.FunctionCall{ID: "calendar-call", Name: "calendar"})
	agent.ChatCtx.Append(&llm.FunctionCallOutput{ID: "calendar-output", Name: "calendar"})

	if err := agent.UpdateTools(context.Background(), []llm.Tool{&agentTestTool{name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools error = %v, want nil", err)
	}

	got := chatItemIDs(agent.ChatCtx.Items)
	want := []string{"msg_1", "lookup-call", "lookup-output"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("agent ChatCtx item IDs = %q, want %q", got, want)
	}
}

func TestAgentUpdateToolsDeduplicatesByToolID(t *testing.T) {
	agent := NewAgent("help")
	first := &agentTestTool{id: "lookup", name: "lookup"}
	replacement := &agentTestTool{id: "lookup", name: "lookup_v2"}

	if err := agent.UpdateTools(context.Background(), []llm.Tool{first, replacement}); err != nil {
		t.Fatalf("UpdateTools error = %v, want nil", err)
	}

	if len(agent.Tools) != 1 {
		t.Fatalf("len(agent.Tools) = %d, want 1", len(agent.Tools))
	}
	if agent.Tools[0] != replacement {
		t.Fatalf("agent.Tools[0] = %p, want replacement %p", agent.Tools[0], replacement)
	}
}

func TestAgentTaskCompleteIsOneTime(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	task.Complete("first")

	returned := make(chan struct{})
	go func() {
		task.Complete("second")
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("second Complete blocked, want no-op after task completion")
	}

	got, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny error = %v, want nil", err)
	}
	if got != "first" {
		t.Fatalf("WaitAny result = %v, want first", got)
	}
}

func TestAgentTaskFailIsOneTime(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	firstErr := errors.New("first failure")
	task.Fail(firstErr)

	returned := make(chan struct{})
	go func() {
		task.Fail(errors.New("second failure"))
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("second Fail blocked, want no-op after task failure")
	}

	got, err := task.WaitAny(context.Background())
	if err != firstErr {
		t.Fatalf("WaitAny error = %v, want %v", err, firstErr)
	}
	if got != nil {
		t.Fatalf("WaitAny result = %v, want nil", got)
	}
}

func TestAgentTaskDoneReflectsCompletion(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	if task.Done() {
		t.Fatal("Done() = true, want false before completion")
	}

	task.Complete("done")

	if !task.Done() {
		t.Fatal("Done() = false, want true after Complete")
	}
}

func TestAgentTaskDoneReflectsFailure(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	task.Fail(errors.New("failed"))

	if !task.Done() {
		t.Fatal("Done() = false, want true after Fail")
	}
}

func TestAgentTaskCancelFailsWithToolError(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	task.ID = "task_1"

	task.Cancel()

	if !task.Done() {
		t.Fatal("Done() = false, want true after Cancel")
	}
	got, err := task.WaitAny(context.Background())
	if got != nil {
		t.Fatalf("WaitAny result = %v, want nil", got)
	}
	var toolErr llm.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("WaitAny error = %T %v, want llm.ToolError", err, err)
	}
	if toolErr.Message != "AgentTask task_1 is cancelled" {
		t.Fatalf("ToolError message = %q, want cancellation message", toolErr.Message)
	}
}

func TestAgentTaskCancelAfterCompleteDoesNotOverrideResult(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	task.Complete("done")

	task.Cancel()

	got, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny error = %v, want nil", err)
	}
	if got != "done" {
		t.Fatalf("WaitAny result = %v, want done", got)
	}
}

func chatItemIDs(items []llm.ChatItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GetID())
	}
	return ids
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
