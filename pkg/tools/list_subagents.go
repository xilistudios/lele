package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ListSubagentsTool returns all subagent tasks known to the SubagentManager.
// By default only active tasks (running or needs_context) are shown.
// Set include_completed=true to also list completed/failed/cancelled tasks.
type ListSubagentsTool struct {
	manager *SubagentManager
}

func NewListSubagentsTool(manager *SubagentManager) *ListSubagentsTool {
	return &ListSubagentsTool{manager: manager}
}

func (t *ListSubagentsTool) Name() string {
	return "list_active_subagents"
}

func (t *ListSubagentsTool) Description() string {
	return "List all active subagent tasks (running or waiting for context). Returns task IDs, status, labels, agents, and summaries. Set include_completed=true to also see finished tasks. Use this to check on spawned subagents before waiting for them or to find a task_id for wait_for_subagent."
}

func (t *ListSubagentsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, also include completed/failed/cancelled tasks (default: false)",
				"default":     false,
			},
		},
	}
}

func (t *ListSubagentsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.manager == nil {
		return ErrorResult("Subagent manager not configured")
	}

	includeCompleted, _ := args["include_completed"].(bool)

	allTasks := t.manager.ListTasks()
	if len(allTasks) == 0 {
		return SilentResult("No subagent tasks found.")
	}

	var filtered []*SubagentTask
	for _, task := range allTasks {
		if includeCompleted {
			filtered = append(filtered, task)
			continue
		}
		if task.Status == SubagentStatusRunning || task.Status == SubagentStatusNeedsContext {
			filtered = append(filtered, task)
		}
	}

	if len(filtered) == 0 {
		if includeCompleted {
			return SilentResult("No subagent tasks found.")
		}
		return SilentResult("No active subagents. All tasks have completed. Set include_completed=true to see finished tasks.")
	}

	lines := make([]string, 0, len(filtered)+2)
	if includeCompleted {
		lines = append(lines, "All subagent tasks:")
	} else {
		lines = append(lines, "Active subagents:")
	}

	for _, task := range filtered {
		line := fmt.Sprintf("- %s [%s]", task.ID, task.Status)
		if task.Label != "" {
			line += fmt.Sprintf(" %s", task.Label)
		}
		if task.AgentID != "" {
			line += fmt.Sprintf(" (agent: %s)", task.AgentID)
		}
		if task.Summary != "" {
			line += fmt.Sprintf(": %s", task.Summary)
		}
		if task.ContextRequest != "" {
			line += fmt.Sprintf(" [needs: %s]", task.ContextRequest)
		}
		// Show elapsed time for running tasks.
		if task.Status == SubagentStatusRunning && task.Created > 0 {
			elapsed := time.Since(time.UnixMilli(task.Created)).Round(time.Second)
			line += fmt.Sprintf(" (running %s)", elapsed)
		}
		lines = append(lines, line)
	}

	lines = append(lines, fmt.Sprintf("\nTotal: %d task(s)", len(filtered)))

	return SilentResult(strings.Join(lines, "\n"))
}
