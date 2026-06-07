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

	if _, err := tool.Execute(context.Background(), `{"task_ids":["first"]}`); err != nil {
		t.Fatalf("Execute() error = %v, want nil after completing active task", err)
	}
	_, err := activeTask.WaitAny(context.Background())
	var outErr *OutOfScopeError
	if !errors.As(err, &outErr) {
		t.Fatalf("active task error = %T, want OutOfScopeError", err)
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
