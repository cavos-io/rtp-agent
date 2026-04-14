package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cavos-io/conversation-worker/core/agent"
	"github.com/cavos-io/conversation-worker/library/logger"
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
		g.mu.Unlock()

		// In Python, it updates chat ctx and tools
		// Here we assume the agent task handles its own execution when Start is called

		logger.Logger.Infow("Running task in group", "taskID", taskID)		
		// For parity, we should add out_of_scope tool to current task
		g.currentTask.GetAgent().Tools = append(g.currentTask.GetAgent().Tools, &outOfScopeTool{group: g, activeTaskID: taskID})

		if waiter, ok := g.currentTask.(agent.TaskWaiter); ok {
			result, err := waiter.WaitAny(context.Background())
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
			// If it's not a TaskWaiter, we just assume it completed (fallback)
			results[taskID] = nil
		}
	}

	g.Complete(&TaskGroupResult{TaskResults: results})
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

func (t *outOfScopeTool) Execute(ctx context.Context, args any) (any, error) {
	m, _ := args.(map[string]any)
	taskIDsRaw, ok := m["task_ids"].([]interface{})
	if !ok {
		return "", fmt.Errorf("invalid or missing task_ids array")
	}
	var taskIDs []string
	for _, raw := range taskIDsRaw {
		if id, isStr := raw.(string); isStr {
			taskIDs = append(taskIDs, id)
		}
	}

	return "", &OutOfScopeError{TargetTaskIDs: taskIDs}
}
