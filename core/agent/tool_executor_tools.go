package agent

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/llm"
)

const (
	cancelTaskToolName      = "lk_agents_cancel_task"
	getRunningTasksToolName = "lk_agents_get_running_tasks"
)

type cancelTaskTool struct{}

func (cancelTaskTool) ID() string { return cancelTaskToolName }

func (cancelTaskTool) Name() string { return cancelTaskToolName }

func (cancelTaskTool) Description() string { return "Cancel a running tool call by call_id." }

func (cancelTaskTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"call_id": map[string]any{
				"type":        "string",
				"description": "The running tool call_id to cancel.",
			},
		},
		"required": []string{"call_id"},
	}
}

func (cancelTaskTool) Execute(context.Context, string) (string, error) {
	return "", llm.NewToolError("Task not found")
}

type getRunningTasksTool struct{}

func (getRunningTasksTool) ID() string { return getRunningTasksToolName }

func (getRunningTasksTool) Name() string { return getRunningTasksToolName }

func (getRunningTasksTool) Description() string {
	return "Get the list of running tool calls that are cancellable."
}

func (getRunningTasksTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (getRunningTasksTool) Execute(context.Context, string) (string, error) {
	return "[]", nil
}

func appendToolExecutorHelperTools(tools []llm.Tool) []llm.Tool {
	if hasCancellableLLMTool(tools) {
		return append(tools, cancelTaskTool{}, getRunningTasksTool{})
	}
	return tools
}

func hasCancellableTool(tools []interface{}) bool {
	for _, tool := range tools {
		if functionTool, ok := tool.(llm.Tool); ok {
			if isNilAgentTool(functionTool) {
				continue
			}
			if llm.ToolHasFlag(functionTool, llm.ToolFlagCancellable) {
				return true
			}
			if toolset, ok := functionTool.(llm.Toolset); ok && hasCancellableLLMTool(toolset.Tools()) {
				return true
			}
		}
		if toolset, ok := tool.(llm.Toolset); ok {
			if hasCancellableLLMTool(toolset.Tools()) {
				return true
			}
		}
	}
	return false
}

func hasCancellableLLMTool(tools []llm.Tool) bool {
	for _, tool := range tools {
		if isNilAgentTool(tool) {
			continue
		}
		if llm.ToolHasFlag(tool, llm.ToolFlagCancellable) {
			return true
		}
		if toolset, ok := tool.(llm.Toolset); ok && hasCancellableLLMTool(toolset.Tools()) {
			return true
		}
	}
	return false
}
