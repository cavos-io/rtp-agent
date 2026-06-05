package workflows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/agent"
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
}

func TestTaskGroupOutOfScopeToolReturnsVisitedTargets(t *testing.T) {
	group := NewTaskGroup(false, false)
	group.Add("first", "Collect first value", nil)
	group.Add("second", "Collect second value", nil)
	group.VisitedTasks["first"] = struct{}{}

	tool := group.buildOutOfScopeTool("second")
	if tool == nil {
		t.Fatal("buildOutOfScopeTool() = nil, want regression tool for visited tasks")
	}

	_, err := tool.Execute(context.Background(), `{"task_ids":["first"]}`)
	var outErr *OutOfScopeError
	if !errors.As(err, &outErr) {
		t.Fatalf("Execute() error = %T, want OutOfScopeError", err)
	}
	if len(outErr.TargetTaskIDs) != 1 || outErr.TargetTaskIDs[0] != "first" {
		t.Fatalf("OutOfScopeError.TargetTaskIDs = %#v, want [first]", outErr.TargetTaskIDs)
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
