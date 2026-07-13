package workflows

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
)

func TestTaskGroupBuildOutOfScopeToolReturnsNilBeforeVisitedTasks(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)

	if tool := group.buildOutOfScopeTool("first"); tool != nil {
		t.Fatalf("buildOutOfScopeTool() = %T, want nil before any task is visited", tool)
	}
}

func TestTaskGroupOutOfScopeToolRejectsInvalidTargets(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	_, err := tool.Execute(context.Background(), `{"task_ids":["missing"]}`)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid target error")
	}
	var outErr *OutOfScopeError
	if errors.As(err, &outErr) {
		t.Fatalf("Execute() error = %T, want validation error before OutOfScopeError", err)
	}
	var toolErr llm.ToolError
	if !errors.As(err, &toolErr) {
		t.Fatalf("Execute() error = %T, want ToolError", err)
	}
	if toolErr.Message != "unable to regress, invalid task id missing" {
		t.Fatalf("ToolError.Message = %q, want reference invalid-target message", toolErr.Message)
	}
}

func TestTaskGroupOutOfScopeToolParametersExposeVisitedTargetEnum(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.Add("third", "Collect third value", nil)
	group.VisitedTasks["first"] = struct{}{}
	group.VisitedTasks["second"] = struct{}{}

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	taskIDs, ok := properties["task_ids"].(map[string]any)
	if !ok {
		t.Fatalf("task_ids schema = %#v, want map", properties["task_ids"])
	}
	items, ok := taskIDs["items"].(map[string]any)
	if !ok {
		t.Fatalf("task_ids.items = %#v, want map", taskIDs["items"])
	}
	enum, ok := items["enum"].([]string)
	if !ok {
		t.Fatalf("task_ids.items.enum = %#v, want []string", items["enum"])
	}
	if len(enum) != 1 || enum[0] != "first" {
		t.Fatalf("task_ids.items.enum = %#v, want [first]", enum)
	}
}

func TestTaskGroupOutOfScopeToolParametersUseReferenceDescription(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	properties, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v, want map", tool.Parameters()["properties"])
	}
	taskIDs, ok := properties["task_ids"].(map[string]any)
	if !ok {
		t.Fatalf("task_ids schema = %#v, want map", properties["task_ids"])
	}
	want := "The IDs of the tasks requested"
	if got := taskIDs["description"]; got != want {
		t.Fatalf("task_ids.description = %#v, want %q", got, want)
	}
}

func TestTaskGroupOutOfScopeToolUsesReferenceDescription(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	want := `Call to regress to other tasks according to what the user requested to modify, return the corresponding task ids. For example, if the user wants to change their email and there is a task with id "email_task" with a description of "Collect the user's email", return the id ("get_email_task").If the user requests to regress to multiple tasks, such as changing their phone number and email, return both task ids in the order they were requested.The following are the IDs and their corresponding task description. {"first":"Collect first value"}`
	if got := tool.Description(); got != want {
		t.Fatalf("Description = %q, want %q", got, want)
	}
}

func TestTaskGroupOutOfScopeToolDescriptionPreservesRegisteredTaskOrder(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("phone", "Collect phone number", nil)
	group.Add("email", "Collect email address", nil)
	group.Add("address", "Collect address", nil)
	group.VisitedTasks["phone"] = struct{}{}
	group.VisitedTasks["email"] = struct{}{}

	tool := group.buildOutOfScopeTool("address")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	wantSuffix := `The following are the IDs and their corresponding task description. {"phone":"Collect phone number","email":"Collect email address"}`
	if got := tool.Description(); !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("Description = %q, want suffix %q", got, wantSuffix)
	}
}

func TestTaskGroupOutOfScopeToolCompletesActiveTaskWithVisitedTargets(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}
	activeTask := newCompletingTask("second")
	group.currentTask = activeTask

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	out, err := tool.Execute(context.Background(), `{"task_ids":["first"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil after completing active task", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty output like reference out_of_scope", out)
	}
	_, err = activeTask.WaitAny(context.Background())
	var outErr *OutOfScopeError
	if !errors.As(err, &outErr) {
		t.Fatalf("active task error = %T, want OutOfScopeError", err)
	}
	if len(outErr.TargetTaskIDs) != 1 || outErr.TargetTaskIDs[0] != "first" {
		t.Fatalf("OutOfScopeError.TargetTaskIDs = %#v, want [first]", outErr.TargetTaskIDs)
	}
}

func TestTaskGroupOutOfScopeToolNoopsWhenActiveTaskDone(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}
	group.currentTask = newCompletedTask("second done")

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	out, err := tool.Execute(context.Background(), `{"task_ids":["first"]}`)
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil when active task is already done", err)
	}
	if out != "" {
		t.Fatalf("Execute() output = %q, want empty no-op output", out)
	}
}

func TestTaskGroupStartsChildTaskBeforeWaiting(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("child", "Completes on enter", func() agent.AgentInterface {
		return newCompletingTask("done")
	})
	session := agent.NewAgentSession(group, nil, agent.AgentSessionOptions{})
	group.Agent.Start(session, group)
	defer group.Agent.GetActivity().Stop()

	select {
	case result := <-group.Result:
		if result.TaskResults["child"] != "done" {
			t.Fatalf("TaskResults[child] = %#v, want done", result.TaskResults["child"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group to complete child task")
	}
}

func TestTaskGroupAddReplacesDuplicateTaskID(t *testing.T) {
	group := NewTaskGroup(false, false)
	firstRuns := 0
	secondRuns := 0
	group.Add("child", "Original child", func() agent.AgentInterface {
		firstRuns++
		return newCompletedTask("old")
	})
	group.Add("child", "Replacement child", func() agent.AgentInterface {
		secondRuns++
		return newCompletedTask("new")
	})

	group.OnEnter()

	select {
	case result := <-group.Result:
		if firstRuns != 0 || secondRuns != 1 {
			t.Fatalf("runs = first:%d second:%d, want replacement factory only once", firstRuns, secondRuns)
		}
		if result.TaskResults["child"] != "new" {
			t.Fatalf("TaskResults[child] = %#v, want replacement result", result.TaskResults["child"])
		}
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want replacement task result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group duplicate-id result")
	}
}

func TestTaskGroupOutOfScopeRerunsTargetAndActiveTask(t *testing.T) {
	group := NewTaskGroup(false, false)
	firstRuns := 0
	secondRuns := 0
	group.Add("first", "Collect first value", func() agent.AgentInterface {
		firstRuns++
		return newCompletedTask("first-" + itoa(firstRuns))
	})
	group.Add("second", "Collect second value", func() agent.AgentInterface {
		secondRuns++
		if secondRuns == 1 {
			return newFailedTask(&OutOfScopeError{TargetTaskIDs: []string{"first"}})
		}
		return newCompletedTask("second-" + itoa(secondRuns))
	})

	group.OnEnter()

	select {
	case result := <-group.Result:
		if firstRuns != 2 || secondRuns != 2 {
			t.Fatalf("runs = first:%d second:%d, want first:2 second:2", firstRuns, secondRuns)
		}
		if result.TaskResults["first"] != "first-2" || result.TaskResults["second"] != "second-2" {
			t.Fatalf("TaskResults = %#v, want rerun target and active task results", result.TaskResults)
		}
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want completed result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}
}

func TestTaskGroupReturnExceptionsRecordsErrorAndContinues(t *testing.T) {
	group := NewTaskGroup(false, true)
	group.Add("first", "Fails", func() agent.AgentInterface {
		return newFailedTask(errors.New("first failed"))
	})
	group.Add("second", "Completes", func() agent.AgentInterface {
		return newCompletedTask("second done")
	})

	group.OnEnter()

	select {
	case result := <-group.Result:
		if err, ok := result.TaskResults["first"].(error); !ok || err.Error() != "first failed" {
			t.Fatalf("TaskResults[first] = %#v, want recorded error", result.TaskResults["first"])
		}
		if result.TaskResults["second"] != "second done" {
			t.Fatalf("TaskResults[second] = %#v, want second done", result.TaskResults["second"])
		}
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want error recorded in result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}
}

func TestTaskGroupOnTaskCompletedReceivesReferenceEvent(t *testing.T) {
	group := NewTaskGroup(false, false)
	child := newCompletedTask("child done")
	var event TaskCompletedEvent
	group.OnTaskCompletedEvent = func(ev TaskCompletedEvent) error {
		event = ev
		return nil
	}
	group.Add("child", "Completes", func() agent.AgentInterface {
		return child
	})

	group.OnEnter()

	select {
	case <-group.Result:
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want completed result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}
	if event.AgentTask != child {
		t.Fatalf("TaskCompletedEvent.AgentTask = %#v, want child task", event.AgentTask)
	}
	if event.TaskID != "child" {
		t.Fatalf("TaskCompletedEvent.TaskID = %q, want child", event.TaskID)
	}
	if event.Result != "child done" {
		t.Fatalf("TaskCompletedEvent.Result = %#v, want child done", event.Result)
	}
}

func TestTaskGroupCopiesChatContextToChildTask(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.ChatCtx.Append(&llm.ChatMessage{
		ID:      "group-user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "known group context"}},
	})
	child := newChatContextTask("child-done")
	group.Add("child", "Reads shared context", func() agent.AgentInterface {
		return child
	})
	session := agent.NewAgentSession(group, nil, agent.AgentSessionOptions{})
	group.Agent.Start(session, group)
	defer group.Agent.GetActivity().Stop()

	select {
	case <-group.Result:
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want completed result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}

	if child.seenGroupItem == nil {
		t.Fatalf("child ChatCtx items = %#v, want copied group item", child.ChatCtx.Items)
	}
	if child.seenGroupItem.TextContent() != "known group context" {
		t.Fatalf("child saw group text = %q, want known group context", child.seenGroupItem.TextContent())
	}
}

func TestTaskGroupMergesCompletedChildChatContext(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.ChatCtx.Append(&llm.ChatMessage{
		ID:      "group-user",
		Role:    llm.ChatRoleUser,
		Content: []llm.ChatContent{{Text: "known group context"}},
	})
	child := newChatContextTask("child-done")
	group.Add("child", "Produces child context", func() agent.AgentInterface {
		return child
	})
	session := agent.NewAgentSession(group, nil, agent.AgentSessionOptions{})
	group.Agent.Start(session, group)
	defer group.Agent.GetActivity().Stop()

	select {
	case <-group.Result:
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want completed result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}

	if got := group.ChatCtx.GetByID("child-assistant"); got == nil {
		t.Fatalf("group ChatCtx items = %#v, want merged child assistant message", group.ChatCtx.Items)
	}
	if got := group.ChatCtx.GetByID("child-instructions"); got != nil {
		t.Fatalf("group ChatCtx instruction item = %#v, want excluded from child merge", got)
	}
}

func TestTaskGroupSummarizesMergedChatContext(t *testing.T) {
	group := NewTaskGroup(true, false)
	child := newChatContextTask("child-done")
	group.Add("child", "Produces child context", func() agent.AgentInterface {
		return child
	})
	summaryLLM := &taskGroupSummaryLLM{response: "collected child context"}
	session := agent.NewAgentSession(group, nil, agent.AgentSessionOptions{})
	session.LLM = summaryLLM
	group.Agent.Start(session, group)
	defer group.Agent.GetActivity().Stop()

	select {
	case <-group.Result:
	case err := <-group.Err:
		t.Fatalf("group failed with %v, want completed result", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task group result")
	}

	if len(summaryLLM.requests) != 1 {
		t.Fatalf("summary LLM requests = %d, want 1", len(summaryLLM.requests))
	}
	summary := findTaskGroupSummaryMessage(group.ChatCtx)
	if summary == nil {
		t.Fatalf("group ChatCtx items = %#v, want summary message", group.ChatCtx.Items)
	}
	if got := summary.TextContent(); got != "<chat_history_summary>\ncollected child context\n</chat_history_summary>" {
		t.Fatalf("summary text = %q, want wrapped LLM summary", got)
	}
	if got := group.ChatCtx.GetByID("child-assistant"); got != nil {
		t.Fatalf("group ChatCtx child assistant item = %#v, want summarized away", got)
	}
}

type completingTask struct {
	agent.AgentTask[string]
	value string
}

func newCompletingTask(value string) *completingTask {
	return &completingTask{
		AgentTask: *agent.NewAgentTask[string]("complete on enter"),
		value:     value,
	}
}

func (t *completingTask) OnEnter() {
	_ = t.Complete(t.value)
}

func newCompletedTask(value string) *completingTask {
	task := newCompletingTask(value)
	_ = task.Complete(value)
	return task
}

type failingTask struct {
	agent.AgentTask[string]
}

func newFailedTask(err error) *failingTask {
	task := &failingTask{AgentTask: *agent.NewAgentTask[string]("failed")}
	_ = task.Fail(err)
	return task
}

type chatContextTask struct {
	agent.AgentTask[string]
	value         string
	seenGroupItem *llm.ChatMessage
}

func newChatContextTask(value string) *chatContextTask {
	return &chatContextTask{
		AgentTask: *agent.NewAgentTask[string]("chat context task"),
		value:     value,
	}
}

func (t *chatContextTask) OnEnter() {
	if msg, ok := t.ChatCtx.GetByID("group-user").(*llm.ChatMessage); ok {
		t.seenGroupItem = msg
	}
	t.ChatCtx.Append(&llm.ChatMessage{
		ID:      "child-instructions",
		Role:    llm.ChatRoleSystem,
		Content: []llm.ChatContent{{Text: "child instructions"}},
	})
	t.ChatCtx.Append(&llm.ChatMessage{
		ID:      "child-assistant",
		Role:    llm.ChatRoleAssistant,
		Content: []llm.ChatContent{{Text: "child response"}},
	})
	_ = t.Complete(t.value)
}

type taskGroupSummaryLLM struct {
	response string
	requests []*llm.ChatContext
}

func (f *taskGroupSummaryLLM) Chat(_ context.Context, chatCtx *llm.ChatContext, _ ...llm.ChatOption) (llm.LLMStream, error) {
	f.requests = append(f.requests, chatCtx)
	return &taskGroupSummaryStream{
		chunks: []*llm.ChatChunk{{Delta: &llm.ChoiceDelta{Content: f.response}}},
	}, nil
}

type taskGroupSummaryStream struct {
	chunks []*llm.ChatChunk
	index  int
}

func (s *taskGroupSummaryStream) Next() (*llm.ChatChunk, error) {
	if s.index >= len(s.chunks) {
		return nil, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++
	return chunk, nil
}

func (s *taskGroupSummaryStream) Close() error { return nil }

func findTaskGroupSummaryMessage(chatCtx *llm.ChatContext) *llm.ChatMessage {
	if chatCtx == nil {
		return nil
	}
	for _, item := range chatCtx.Items {
		msg, ok := item.(*llm.ChatMessage)
		if !ok {
			continue
		}
		if isSummary, _ := msg.Extra["is_summary"].(bool); isSummary {
			return msg
		}
	}
	return nil
}
