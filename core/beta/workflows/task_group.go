package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cavos-io/rtp-agent/core/agent"
	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/library/logger"
)

type TaskGroupResult struct {
	TaskResults map[string]any
}

type FactoryInfo struct {
	ID          string
	Description string
	TaskFactory func() agent.AgentInterface
}

type OutOfScopeError struct {
	TargetTaskIDs []string
}

func (e *OutOfScopeError) Error() string {
	return fmt.Sprintf("out of scope: regression to %v", e.TargetTaskIDs)
}

type TaskGroup struct {
	agent.AgentTask[*TaskGroupResult]
	SummarizeChatCtx bool
	ReturnExceptions bool
	VisitedTasks     map[string]struct{}
	RegisteredTasks  []FactoryInfo
	OnTaskCompleted  func(taskID string, result any) error

	currentTask agent.AgentInterface
	mu          sync.Mutex
}

func NewTaskGroup(summarize bool, returnExceptions bool) *TaskGroup {
	t := &TaskGroup{
		AgentTask:        *agent.NewAgentTask[*TaskGroupResult]("*empty*"),
		SummarizeChatCtx: summarize,
		ReturnExceptions: returnExceptions,
		VisitedTasks:     make(map[string]struct{}),
		RegisteredTasks:  make([]FactoryInfo, 0),
	}
	return t
}

func (g *TaskGroup) Add(id, description string, factory func() agent.AgentInterface) *TaskGroup {
	g.RegisteredTasks = append(g.RegisteredTasks, FactoryInfo{
		ID:          id,
		Description: description,
		TaskFactory: factory,
	})
	return g
}

func (g *TaskGroup) OnEnter() {
	go g.runTasks()
}

func (g *TaskGroup) runTasks() {
	taskStack := make([]string, 0)
	for _, info := range g.RegisteredTasks {
		taskStack = append(taskStack, info.ID)
	}

	results := make(map[string]any)

	for len(taskStack) > 0 {
		taskID := taskStack[0]
		taskStack = taskStack[1:]

		var factory FactoryInfo
		for _, info := range g.RegisteredTasks {
			if info.ID == taskID {
				factory = info
				break
			}
		}

		g.mu.Lock()
		g.currentTask = factory.TaskFactory()
		g.VisitedTasks[taskID] = struct{}{}
		currentTask := g.currentTask
		g.mu.Unlock()

		logger.Logger.Infow("Running task in group", "taskID", taskID)
		if tool := g.buildOutOfScopeTool(taskID); tool != nil {
			currentTask.GetAgent().Tools = append(currentTask.GetAgent().Tools, tool)
		}

		var activity *agent.AgentActivity
		if groupActivity := g.Agent.GetActivity(); groupActivity != nil && groupActivity.Session != nil {
			activity = currentTask.GetAgent().Start(groupActivity.Session, currentTask)
		}

		if waiter, ok := currentTask.(agent.TaskWaiter); ok {
			result, err := waiter.WaitAny(context.Background())
			if activity != nil {
				activity.Stop()
			}
			if err != nil {
				if outErr, isOutErr := err.(*OutOfScopeError); isOutErr {
					// Regression logic
					taskStack = append(outErr.TargetTaskIDs, taskStack...)
					continue
				}
				g.Fail(err)
				return
			}
			results[taskID] = result

			if g.OnTaskCompleted != nil {
				if err := g.OnTaskCompleted(taskID, result); err != nil {
					g.Fail(err)
					return
				}
			}
		} else {
			if activity != nil {
				activity.Stop()
			}
			// If it's not a TaskWaiter, we just assume it completed (fallback)
			results[taskID] = nil
		}
	}

	g.Complete(&TaskGroupResult{TaskResults: results})
}

func (g *TaskGroup) buildOutOfScopeTool(activeTaskID string) llm.Tool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if len(g.VisitedTasks) == 0 {
		return nil
	}
	for taskID := range g.VisitedTasks {
		if taskID != activeTaskID {
			return &outOfScopeTool{group: g, activeTaskID: activeTaskID}
		}
	}
	return nil
}

type outOfScopeTool struct {
	group        *TaskGroup
	activeTaskID string
}

func (t *outOfScopeTool) ID() string   { return "out_of_scope" }
func (t *outOfScopeTool) Name() string { return "out_of_scope" }
func (t *outOfScopeTool) Description() string {
	t.group.mu.Lock()
	defer t.group.mu.Unlock()

	taskRepr := make(map[string]string)
	for _, info := range t.group.RegisteredTasks {
		if _, ok := t.group.VisitedTasks[info.ID]; ok && info.ID != t.activeTaskID {
			taskRepr[info.ID] = info.Description
		}
	}

	reprJSON, _ := json.Marshal(taskRepr)

	return fmt.Sprintf(`Call to regress to other tasks according to what the user requested to modify, return the corresponding task ids. 
For example, if the user wants to change their email and there is a task with id "email_task" with a description of "Collect the user's email", return the id ("get_email_task").
If the user requests to regress to multiple tasks, such as changing their phone number and email, return both task ids in the order they were requested.
The following are the IDs and their corresponding task description. %s`, string(reprJSON))
}

func (t *outOfScopeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_ids": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"task_ids"},
	}
}

func (t *outOfScopeTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", err
	}
	if err := t.validateTaskIDs(params.TaskIDs); err != nil {
		return "", err
	}

	return "", &OutOfScopeError{TargetTaskIDs: params.TaskIDs}
}

func (t *outOfScopeTool) validateTaskIDs(taskIDs []string) error {
	t.group.mu.Lock()
	defer t.group.mu.Unlock()

	for _, taskID := range taskIDs {
		if taskID == t.activeTaskID {
			return fmt.Errorf("unable to regress, invalid task id %s", taskID)
		}
		if _, visited := t.group.VisitedTasks[taskID]; !visited {
			return fmt.Errorf("unable to regress, invalid task id %s", taskID)
		}
		if !t.group.registeredTask(taskID) {
			return fmt.Errorf("unable to regress, invalid task id %s", taskID)
		}
	}
	return nil
}

func (g *TaskGroup) registeredTask(taskID string) bool {
	for _, info := range g.RegisteredTasks {
		if info.ID == taskID {
			return true
		}
	}
	return false
}
