package workflows

import (
	"context"
	"errors"
	"testing"
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
