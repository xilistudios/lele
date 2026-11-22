package tools

import (
	"context"
	"fmt"
	"strings"
)

type CancelSubagentTool struct {
	manager *SubagentManager
}

func NewCancelSubagentTool(manager *SubagentManager) *CancelSubagentTool {
	return &CancelSubagentTool{manager: manager}
}

func (t *CancelSubagentTool) Name() string {
	return "cancel_subagent"
}

func (t *CancelSubagentTool) Description() string {
	return "Cancel a running or pending subagent task. Use this to stop a subagent that is stuck, no longer needed, or should be restarted with different parameters. The task must be in running, pending, or needs_context status to be cancelled."
}

func (t *CancelSubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "The subagent task ID to cancel (e.g., 'subagent-1')",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *CancelSubagentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return ErrorResult("task_id is required")
	}

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured")
	}

	task, ok := t.manager.GetTask(taskID)
	if !ok {
		return ErrorResult(fmt.Sprintf("Subagent task not found: %s", taskID))
	}

	if isSubagentTerminal(task.Status) {
		return SilentResult(fmt.Sprintf(
			"Subagent task %s is already in terminal status (%s) and cannot be cancelled.",
			taskID, task.Status,
		))
	}

	if t.manager.StopTask(taskID) {
		return SilentResult(fmt.Sprintf(
			"Subagent task %s has been cancelled.",
			taskID,
		))
	}

	return ErrorResult(fmt.Sprintf(
		"Failed to cancel subagent task %s (current status: %s)",
		taskID, task.Status,
	))
}
