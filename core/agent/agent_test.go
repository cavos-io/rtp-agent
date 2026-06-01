package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/llm"
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

func TestAgentUpdateInstructionsWhileRunningRecordsConfigUpdate(t *testing.T) {
	agent := NewAgent("old")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	if err := agent.UpdateInstructions(context.Background(), "new"); err != nil {
		t.Fatalf("UpdateInstructions error = %v, want nil", err)
	}

	if agent.Instructions != "new" {
		t.Fatalf("agent instructions = %q, want new", agent.Instructions)
	}
	assertLastInstructionUpdate(t, agent.ChatCtx, "new")
	assertLastInstructionUpdate(t, session.ChatCtx, "new")
}

func TestAgentUpdateInstructionsWhileRunningAddsInstructionMessage(t *testing.T) {
	agent := NewAgent("old")
	agent.ChatCtx.Append(&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	if err := agent.UpdateInstructions(context.Background(), "new"); err != nil {
		t.Fatalf("UpdateInstructions error = %v, want nil", err)
	}

	if len(agent.ChatCtx.Items) < 2 {
		t.Fatalf("chat items = %d, want at least instruction and existing user message", len(agent.ChatCtx.Items))
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first item = %T, want *llm.ChatMessage", agent.ChatCtx.Items[0])
	}
	if msg.ID != agentInstructionsMessageID || msg.Role != llm.ChatRoleSystem || msg.TextContent() != "new" {
		t.Fatalf("instruction message = %#v, want system instructions with new text", msg)
	}
}

func TestAgentUpdateInstructionsWhileRunningReplacesInstructionMessage(t *testing.T) {
	agent := NewAgent("old")
	createdAt := time.Unix(10, 0)
	agent.ChatCtx.Append(&llm.ChatMessage{
		ID:        agentInstructionsMessageID,
		Role:      llm.ChatRoleSystem,
		Content:   []llm.ChatContent{{Text: "old"}},
		CreatedAt: createdAt,
	})
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	if err := agent.UpdateInstructions(context.Background(), "new"); err != nil {
		t.Fatalf("UpdateInstructions error = %v, want nil", err)
	}

	instructionCount := 0
	var msg *llm.ChatMessage
	for _, item := range agent.ChatCtx.Items {
		if item.GetID() != agentInstructionsMessageID {
			continue
		}
		instructionCount++
		var ok bool
		msg, ok = item.(*llm.ChatMessage)
		if !ok {
			t.Fatalf("instruction item = %T, want *llm.ChatMessage", item)
		}
	}
	if instructionCount != 1 {
		t.Fatalf("instruction message count = %d, want 1", instructionCount)
	}
	if msg.TextContent() != "new" || !msg.CreatedAt.Equal(createdAt) {
		t.Fatalf("instruction message text/createdAt = %q/%v, want new/%v", msg.TextContent(), msg.CreatedAt, createdAt)
	}
}

func TestAgentUpdateToolsWhileRunningRecordsToolDiffAndFiltersWithSessionTools(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{&agentTestTool{id: "lookup", name: "lookup"}}
	agent.ChatCtx.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})
	agent.ChatCtx.Append(&llm.FunctionCall{ID: "calc-call", Name: "calc"})
	agent.ChatCtx.Append(&llm.FunctionCall{ID: "calendar-call", Name: "calendar"})
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.Tools = []llm.Tool{&agentTestTool{id: "calendar", name: "calendar"}}
	agent.activity = NewAgentActivity(agent, session)

	if err := agent.UpdateTools(context.Background(), []llm.Tool{&agentTestTool{id: "calc", name: "calc"}}); err != nil {
		t.Fatalf("UpdateTools error = %v, want nil", err)
	}

	if len(agent.Tools) != 1 || agent.Tools[0].Name() != "calc" {
		t.Fatalf("agent tools = %#v, want calc only", agent.Tools)
	}
	gotIDs := chatItemIDs(agent.ChatCtx.Items)
	wantIDs := []string{agentInstructionsMessageID, "calc-call", "calendar-call", ""}
	if !stringSlicesEqual(gotIDs, wantIDs) {
		t.Fatalf("agent ChatCtx item IDs = %q, want %q", gotIDs, wantIDs)
	}
	config := assertLastToolUpdate(t, agent.ChatCtx)
	if !stringSlicesEqual(config.ToolsAdded, []string{"calc"}) {
		t.Fatalf("ToolsAdded = %q, want [calc]", config.ToolsAdded)
	}
	if !stringSlicesEqual(config.ToolsRemoved, []string{"lookup"}) {
		t.Fatalf("ToolsRemoved = %q, want [lookup]", config.ToolsRemoved)
	}
	assertLastToolUpdate(t, session.ChatCtx)
}

func TestAgentUpdateChatContextWhileRunningAddsInstructionMessage(t *testing.T) {
	agent := NewAgent("be helpful")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)
	source := llm.NewChatContext()
	source.Append(&llm.ChatMessage{ID: "user", Role: llm.ChatRoleUser, Content: []llm.ChatContent{{Text: "hello"}}})

	if err := agent.UpdateChatContext(context.Background(), source); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want nil", err)
	}

	if got := chatItemIDs(agent.ChatCtx.Items); !stringSlicesEqual(got, []string{agentInstructionsMessageID, "user"}) {
		t.Fatalf("agent ChatCtx item IDs = %q, want instructions then user", got)
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first item = %T, want *llm.ChatMessage", agent.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleSystem || msg.TextContent() != "be helpful" {
		t.Fatalf("instruction message role/text = %s/%q, want system/be helpful", msg.Role, msg.TextContent())
	}
}

func TestAgentUpdateChatContextWhileRunningReplacesInstructionMessage(t *testing.T) {
	agent := NewAgent("new instructions")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)
	createdAt := time.Unix(20, 0)
	source := llm.NewChatContext()
	source.Append(&llm.ChatMessage{
		ID:        agentInstructionsMessageID,
		Role:      llm.ChatRoleSystem,
		Content:   []llm.ChatContent{{Text: "old instructions"}},
		CreatedAt: createdAt,
	})

	if err := agent.UpdateChatContext(context.Background(), source); err != nil {
		t.Fatalf("UpdateChatContext error = %v, want nil", err)
	}

	if len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("chat item count = %d, want 1", len(agent.ChatCtx.Items))
	}
	msg, ok := agent.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("first item = %T, want *llm.ChatMessage", agent.ChatCtx.Items[0])
	}
	if msg.TextContent() != "new instructions" || !msg.CreatedAt.Equal(createdAt) {
		t.Fatalf("instruction message text/createdAt = %q/%v, want new instructions/%v", msg.TextContent(), msg.CreatedAt, createdAt)
	}
}

func TestAgentTaskCompleteIsOneTime(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	if err := task.Complete("first"); err != nil {
		t.Fatalf("Complete(first) error = %v, want nil", err)
	}

	if err := task.Complete("second"); !errors.Is(err, ErrAgentTaskAlreadyDone) {
		t.Fatalf("Complete(second) error = %v, want ErrAgentTaskAlreadyDone", err)
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
	if err := task.Fail(firstErr); err != nil {
		t.Fatalf("Fail(first) error = %v, want nil", err)
	}

	if err := task.Fail(errors.New("second failure")); !errors.Is(err, ErrAgentTaskAlreadyDone) {
		t.Fatalf("Fail(second) error = %v, want ErrAgentTaskAlreadyDone", err)
	}

	got, err := task.WaitAny(context.Background())
	if err != firstErr {
		t.Fatalf("WaitAny error = %v, want %v", err, firstErr)
	}
	if got != nil {
		t.Fatalf("WaitAny result = %v, want nil", got)
	}
}

func TestAgentTaskFailAfterCompleteReturnsAlreadyDone(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete error = %v, want nil", err)
	}

	err := task.Fail(errors.New("late failure"))

	if !errors.Is(err, ErrAgentTaskAlreadyDone) {
		t.Fatalf("Fail after Complete error = %v, want ErrAgentTaskAlreadyDone", err)
	}
	got, waitErr := task.WaitAny(context.Background())
	if waitErr != nil {
		t.Fatalf("WaitAny error = %v, want nil", waitErr)
	}
	if got != "done" {
		t.Fatalf("WaitAny result = %v, want done", got)
	}
}

func TestAgentTaskCompleteAfterFailReturnsAlreadyDone(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	firstErr := errors.New("failed")
	if err := task.Fail(firstErr); err != nil {
		t.Fatalf("Fail error = %v, want nil", err)
	}

	err := task.Complete("late result")

	if !errors.Is(err, ErrAgentTaskAlreadyDone) {
		t.Fatalf("Complete after Fail error = %v, want ErrAgentTaskAlreadyDone", err)
	}
	got, waitErr := task.WaitAny(context.Background())
	if waitErr != firstErr {
		t.Fatalf("WaitAny error = %v, want %v", waitErr, firstErr)
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

	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete error = %v, want nil", err)
	}

	if !task.Done() {
		t.Fatal("Done() = false, want true after Complete")
	}
}

func TestAgentTaskDoneReflectsFailure(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	if err := task.Fail(errors.New("failed")); err != nil {
		t.Fatalf("Fail error = %v, want nil", err)
	}

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
	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete error = %v, want nil", err)
	}

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

func assertLastInstructionUpdate(t *testing.T, chatCtx *llm.ChatContext, instructions string) {
	t.Helper()
	if len(chatCtx.Items) == 0 {
		t.Fatal("chat context has no items")
	}
	config, ok := chatCtx.Items[len(chatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last item = %T, want *llm.AgentConfigUpdate", chatCtx.Items[len(chatCtx.Items)-1])
	}
	if config.Instructions == nil || *config.Instructions != instructions {
		t.Fatalf("config instructions = %v, want %q", config.Instructions, instructions)
	}
}

func assertLastToolUpdate(t *testing.T, chatCtx *llm.ChatContext) *llm.AgentConfigUpdate {
	t.Helper()
	if len(chatCtx.Items) == 0 {
		t.Fatal("chat context has no items")
	}
	config, ok := chatCtx.Items[len(chatCtx.Items)-1].(*llm.AgentConfigUpdate)
	if !ok {
		t.Fatalf("last item = %T, want *llm.AgentConfigUpdate", chatCtx.Items[len(chatCtx.Items)-1])
	}
	return config
}
