package agent

import (
	"context"
	"errors"
	"strings"
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

func TestAgentChatContextReturnsReadOnlyCopy(t *testing.T) {
	agent := NewAgent("help")
	agent.ChatCtx.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})

	got := agent.ChatContext()
	if got == agent.ChatCtx {
		t.Fatal("ChatContext() returned internal context pointer, want copy")
	}
	if !got.Readonly() {
		t.Fatal("ChatContext().Readonly() = false, want reference-style read-only view")
	}
	if len(got.Items) != 1 || got.Items[0].GetID() != "msg_1" {
		t.Fatalf("ChatContext() items = %#v, want copied msg_1", got.Items)
	}

	assertAgentPanics(t, "Append on read-only ChatContext()", func() {
		got.Append(&llm.ChatMessage{ID: "msg_2", Role: llm.ChatRoleAssistant})
	})
	if len(agent.ChatCtx.Items) != 1 {
		t.Fatalf("mutating ChatContext() result changed agent context to %d items, want 1", len(agent.ChatCtx.Items))
	}
}

func assertAgentPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s did not panic", name)
		}
	}()
	fn()
}

func TestAgentChatContextHandlesNilAgentContext(t *testing.T) {
	agent := NewAgent("help")
	agent.ChatCtx = nil

	got := agent.ChatContext()
	if got == nil {
		t.Fatal("ChatContext() = nil, want empty context")
	}
	if len(got.Items) != 0 {
		t.Fatalf("ChatContext() items = %d, want empty context", len(got.Items))
	}
}

func TestAgentLabelReturnsID(t *testing.T) {
	agent := NewAgent("help")
	agent.ID = "agent-support"

	if got := agent.Label(); got != "agent-support" {
		t.Fatalf("Label() = %q, want agent ID", got)
	}
}

func TestNewAgentUsesReferenceDefaultID(t *testing.T) {
	agent := NewAgent("help")

	if agent.ID != "default_agent" {
		t.Fatalf("NewAgent().ID = %q, want default_agent", agent.ID)
	}
	if got := agent.Label(); got != "default_agent" {
		t.Fatalf("Label() = %q, want default_agent", got)
	}
}

func TestAgentHandoffIDUsesDefaultAgentID(t *testing.T) {
	agent := NewAgent("sensitive instructions")

	if got := agentHandoffID(agent); got != "default_agent" {
		t.Fatalf("agentHandoffID() = %q, want default_agent", got)
	}
}

func TestAgentTaskWaitReturnsCompletedResult(t *testing.T) {
	task := NewAgentTask[string]("collect name")
	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	got, err := task.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got != "done" {
		t.Fatalf("Wait() result = %q, want done", got)
	}
}

func TestNewAgentTaskUsesReferenceDefaultID(t *testing.T) {
	task := NewAgentTask[string]("collect name")

	if task.ID != "agent_task" {
		t.Fatalf("NewAgentTask().ID = %q, want agent_task", task.ID)
	}
	if got := task.Label(); got != "agent_task" {
		t.Fatalf("Label() = %q, want agent_task", got)
	}
}

func TestAgentTaskWaitReturnsFailure(t *testing.T) {
	task := NewAgentTask[string]("collect name")
	wantErr := errors.New("failed")
	if err := task.Fail(wantErr); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	got, err := task.Wait(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Wait() error = %v, want %v", err, wantErr)
	}
	if got != "" {
		t.Fatalf("Wait() result = %q, want zero value", got)
	}
}

func TestAgentTaskWaitReturnsContextError(t *testing.T) {
	task := NewAgentTask[string]("collect name")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := task.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context canceled", err)
	}
	if got != "" {
		t.Fatalf("Wait() result = %q, want zero value", got)
	}
}

func TestAgentTaskWaitAnyReturnsCompletedResult(t *testing.T) {
	task := NewAgentTask[int]("collect number")
	if err := task.Complete(7); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	got, err := task.WaitAny(context.Background())
	if err != nil {
		t.Fatalf("WaitAny() error = %v", err)
	}
	if got != 7 {
		t.Fatalf("WaitAny() result = %#v, want 7", got)
	}
}

func TestAgentTaskWaitIsNotReentrant(t *testing.T) {
	task := NewAgentTask[string]("collect name")
	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, err := task.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	got, err := task.Wait(context.Background())
	if !errors.Is(err, ErrAgentTaskAlreadyWaited) {
		t.Fatalf("second Wait() error = %v, want ErrAgentTaskAlreadyWaited", err)
	}
	if gotErr, want := err.Error(), "AgentTask is not re-entrant, await only once"; gotErr != want {
		t.Fatalf("second Wait() error text = %q, want %q", gotErr, want)
	}
	if got != "" {
		t.Fatalf("second Wait() result = %q, want zero value", got)
	}
}

func TestAgentTaskWaitAnyIsNotReentrant(t *testing.T) {
	task := NewAgentTask[string]("collect name")
	if err := task.Complete("done"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, err := task.WaitAny(context.Background()); err != nil {
		t.Fatalf("first WaitAny() error = %v", err)
	}

	got, err := task.WaitAny(context.Background())
	if !errors.Is(err, ErrAgentTaskAlreadyWaited) {
		t.Fatalf("second WaitAny() error = %v, want ErrAgentTaskAlreadyWaited", err)
	}
	if gotErr, want := err.Error(), "AgentTask is not re-entrant, await only once"; gotErr != want {
		t.Fatalf("second WaitAny() error text = %q, want %q", gotErr, want)
	}
	if got != nil {
		t.Fatalf("second WaitAny() result = %#v, want nil", got)
	}
}

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

func TestAgentUpdateChatContextRejectsNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{nil}
	original := agent.ChatCtx
	source := llm.NewChatContext()
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})

	err := agent.UpdateChatContext(context.Background(), source)

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateChatContext error = %v, want nil tool error", err)
	}
	if agent.ChatCtx != original {
		t.Fatal("UpdateChatContext mutated chat context after failed tool validation")
	}
}

func TestAgentUpdateChatContextRejectsTypedNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	var nilTool *agentTestTool
	agent.Tools = []llm.Tool{nilTool}
	original := agent.ChatCtx
	source := llm.NewChatContext()
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})

	err := agent.UpdateChatContext(context.Background(), source)

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateChatContext error = %v, want nil tool error", err)
	}
	if agent.ChatCtx != original {
		t.Fatal("UpdateChatContext mutated chat context after failed tool validation")
	}
}

func TestAgentUpdateChatContextWhileRunningRejectsNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{nil}
	original := agent.ChatCtx
	agent.activity = NewAgentActivity(agent, nil)
	source := llm.NewChatContext()
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})

	err := agent.UpdateChatContext(context.Background(), source)

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateChatContext error = %v, want nil tool error", err)
	}
	if agent.ChatCtx != original {
		t.Fatal("UpdateChatContext mutated chat context after failed tool validation")
	}
}

func TestAgentUpdateChatContextWhileRunningRejectsTypedNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	var nilTool *agentTestTool
	agent.Tools = []llm.Tool{nilTool}
	original := agent.ChatCtx
	agent.activity = NewAgentActivity(agent, nil)
	source := llm.NewChatContext()
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})

	err := agent.UpdateChatContext(context.Background(), source)

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateChatContext error = %v, want nil tool error", err)
	}
	if agent.ChatCtx != original {
		t.Fatal("UpdateChatContext mutated chat context after failed tool validation")
	}
}

func TestAgentUpdateChatCtxMatchesReferenceName(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{&agentTestTool{name: "lookup"}}
	source := llm.NewChatContext()
	source.Append(&llm.ChatMessage{ID: "msg_1", Role: llm.ChatRoleUser})
	source.Append(&llm.FunctionCall{ID: "lookup-call", Name: "lookup"})
	source.Append(&llm.FunctionCall{ID: "calendar-call", Name: "calendar"})

	if err := agent.UpdateChatCtx(context.Background(), source); err != nil {
		t.Fatalf("UpdateChatCtx error = %v, want nil", err)
	}

	got := chatItemIDs(agent.ChatCtx.Items)
	want := []string{"msg_1", "lookup-call"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("agent ChatCtx item IDs = %q, want %q", got, want)
	}
	if agent.ChatCtx == source {
		t.Fatal("UpdateChatCtx reused source context, want copied context")
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

func TestAgentUpdateToolsRejectsNilTool(t *testing.T) {
	agent := NewAgent("help")
	existing := &agentTestTool{id: "lookup", name: "lookup"}
	agent.Tools = []llm.Tool{existing}

	err := agent.UpdateTools(context.Background(), []llm.Tool{nil})

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateTools error = %v, want nil tool error", err)
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != existing {
		t.Fatalf("agent.Tools = %#v, want unchanged existing tool", agent.Tools)
	}
}

func TestAgentUpdateToolsRejectsTypedNilTool(t *testing.T) {
	agent := NewAgent("help")
	existing := &agentTestTool{id: "lookup", name: "lookup"}
	agent.Tools = []llm.Tool{existing}
	var nilTool *agentTestTool

	err := agent.UpdateTools(context.Background(), []llm.Tool{nilTool})

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateTools error = %v, want nil tool error", err)
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != existing {
		t.Fatalf("agent.Tools = %#v, want unchanged existing tool", agent.Tools)
	}
}

func TestAgentUpdateToolsWhileRunningRejectsNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{nil}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	err := agent.UpdateTools(context.Background(), []llm.Tool{&agentTestTool{id: "lookup", name: "lookup"}})

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateTools error = %v, want nil tool error", err)
	}
	if len(agent.Tools) != 1 || agent.Tools[0] != nil {
		t.Fatalf("agent.Tools = %#v, want unchanged nil tool after failed update", agent.Tools)
	}
}

func TestAgentUpdateToolsWhileRunningRejectsTypedNilExistingTool(t *testing.T) {
	agent := NewAgent("help")
	var nilTool *agentTestTool
	agent.Tools = []llm.Tool{nilTool}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	err := agent.UpdateTools(context.Background(), []llm.Tool{&agentTestTool{id: "lookup", name: "lookup"}})

	if err == nil || !strings.Contains(err.Error(), "nil tool") {
		t.Fatalf("UpdateTools error = %v, want nil tool error", err)
	}
	if len(agent.Tools) != 1 || !isNilAgentTool(agent.Tools[0]) {
		t.Fatalf("agent.Tools = %#v, want unchanged typed-nil tool after failed update", agent.Tools)
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

func TestAgentUpdateToolsWhileRunningRecordsSameNameReplacement(t *testing.T) {
	agent := NewAgent("help")
	agent.Tools = []llm.Tool{&agentTestTool{id: "primary_lookup", name: "lookup"}}
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	if err := agent.UpdateTools(context.Background(), []llm.Tool{&agentTestTool{id: "backup_lookup", name: "lookup"}}); err != nil {
		t.Fatalf("UpdateTools error = %v, want nil", err)
	}

	config := assertLastToolUpdate(t, agent.ChatCtx)
	if !stringSlicesEqual(config.ToolsAdded, []string{"lookup"}) {
		t.Fatalf("ToolsAdded = %q, want replacement lookup", config.ToolsAdded)
	}
	if !stringSlicesEqual(config.ToolsRemoved, []string{"lookup"}) {
		t.Fatalf("ToolsRemoved = %q, want replacement lookup", config.ToolsRemoved)
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

func TestAgentUpdateInstructionsRejectsNonMessageInstructionItem(t *testing.T) {
	agent := NewAgent("new instructions")
	agent.ChatCtx.Append(&llm.FunctionCall{ID: agentInstructionsMessageID, Name: "lookup"})
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	agent.activity = NewAgentActivity(agent, session)

	err := agent.UpdateInstructions(context.Background(), "newer instructions")

	if err == nil {
		t.Fatal("UpdateInstructions error = nil, want non-message instruction item error")
	}
	if got, want := err.Error(), "expected the instructions inside the chat_ctx to be of type 'message'"; got != want {
		t.Fatalf("UpdateInstructions error text = %q, want %q", got, want)
	}
}

func TestAgentOnUserTurnExceededGeneratesNonInterruptibleCutIn(t *testing.T) {
	agent := NewAgent("help")
	session := NewAgentSession(agent, nil, AgentSessionOptions{})
	session.activity = NewAgentActivity(agent, session)
	agent.activity = session.activity
	ev := UserTurnExceededEvent{Transcript: "I have been talking for too long"}

	if err := agent.OnUserTurnExceeded(context.Background(), ev); err != nil {
		t.Fatalf("OnUserTurnExceeded error = %v, want nil", err)
	}

	if len(session.activity.speechQueue) != 1 {
		t.Fatalf("speech queue length = %d, want 1", len(session.activity.speechQueue))
	}
	handle := session.activity.speechQueue[0].speech
	if handle.AllowInterruptions {
		t.Fatal("handle.AllowInterruptions = true, want false for default exceeded-turn cut-in")
	}
	if handle.Generation.ToolChoice != "none" {
		t.Fatalf("ToolChoice = %#v, want none", handle.Generation.ToolChoice)
	}
	if handle.Generation.Instructions == nil {
		t.Fatal("Generation.Instructions = nil, want default exceeded-turn instructions")
	}
	if got := handle.Generation.Instructions.AsModality("text").String(); got == "" {
		t.Fatal("Generation.Instructions text is empty, want default exceeded-turn instructions")
	}
	if len(session.ChatCtx.Items) != 1 {
		t.Fatalf("session ChatCtx items = %d, want exceeded transcript committed", len(session.ChatCtx.Items))
	}
	msg, ok := session.ChatCtx.Items[0].(*llm.ChatMessage)
	if !ok {
		t.Fatalf("session ChatCtx item = %T, want *llm.ChatMessage", session.ChatCtx.Items[0])
	}
	if msg.Role != llm.ChatRoleUser || msg.TextContent() != ev.Transcript {
		t.Fatalf("session ChatCtx message role/text = %s/%q, want user/%q", msg.Role, msg.TextContent(), ev.Transcript)
	}
}

func TestAgentTaskCompleteIsOneTime(t *testing.T) {
	task := NewAgentTask[string]("collect data")
	if err := task.Complete("first"); err != nil {
		t.Fatalf("Complete(first) error = %v, want nil", err)
	}

	if err := task.Complete("second"); !errors.Is(err, ErrAgentTaskAlreadyDone) {
		t.Fatalf("Complete(second) error = %v, want ErrAgentTaskAlreadyDone", err)
	} else if got, want := err.Error(), "AgentTask is already done"; got != want {
		t.Fatalf("Complete(second) error text = %q, want %q", got, want)
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
	} else if got, want := err.Error(), "AgentTask is already done"; got != want {
		t.Fatalf("Fail(second) error text = %q, want %q", got, want)
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
	if got, want := err.Error(), "AgentTask is already done"; got != want {
		t.Fatalf("Fail after Complete error text = %q, want %q", got, want)
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
	if got, want := err.Error(), "AgentTask is already done"; got != want {
		t.Fatalf("Complete after Fail error text = %q, want %q", got, want)
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

func TestAgentTaskCancelUsesDefaultID(t *testing.T) {
	task := NewAgentTask[string]("collect data")

	task.Cancel()

	_, err := task.WaitAny(context.Background())
	var toolErr llm.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("WaitAny error = %T %v, want llm.ToolError", err, err)
	}
	if toolErr.Message != "AgentTask agent_task is cancelled" {
		t.Fatalf("ToolError message = %q, want default cancellation message", toolErr.Message)
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
