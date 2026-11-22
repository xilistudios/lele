package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type WaitForSubagentTool struct {
	manager *SubagentManager
}

func NewWaitForSubagentTool(manager *SubagentManager) *WaitForSubagentTool {
	return &WaitForSubagentTool{manager: manager}
}

func (t *WaitForSubagentTool) Name() string {
	return "wait_for_subagent"
}

func (t *WaitForSubagentTool) Description() string {
	return "Wait for a subagent task to complete and return its result. Blocks until the subagent finishes, fails, is cancelled, pauses for context, or the timeout expires. Use this to synchronously collect results from previously spawned subagents instead of relying on async callbacks."
}

func (t *WaitForSubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "The subagent task ID to wait for (e.g., 'subagent-1')",
			},
			"timeout_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum seconds to wait (default: 120, max: 600)",
				"default":     120,
				"minimum":     1,
				"maximum":     600,
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *WaitForSubagentTool) Execute(ctx context.Context, args map[string]interface{}) (result *ToolResult) {
	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return ErrorResult("task_id is required")
	}

	timeoutSeconds := 120
	if ts, ok := args["timeout_seconds"].(float64); ok && ts > 0 {
		timeoutSeconds = int(ts)
	}
	if timeoutSeconds > 600 {
		timeoutSeconds = 600
	}
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured")
	}

	task, ok := t.manager.GetTask(taskID)
	if !ok {
		return ErrorResult(fmt.Sprintf("Subagent task not found: %s", taskID))
	}

	t.manager.AddResultWaiter(taskID)

	defer func() {
		if raced := t.manager.ReleaseResultWaiterAndClaim(taskID); raced != nil {
			if result == nil || result.IsError {
				result = SilentResult(formatSubagentTaskResult(raced))
			}
		}
	}()

	if isSubagentTerminal(task.Status) {
		t.manager.ClaimResult(taskID)
		return SilentResult(formatSubagentTaskResult(task))
	}

	timeout := time.Duration(timeoutSeconds) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	doneCh := task.DoneChannel()
	if doneCh != nil {
		for {
			select {
			case <-ctx.Done():
				return ErrorResult(fmt.Sprintf("Wait for subagent %s interrupted", taskID))
			case <-timer.C:
				return ErrorResult(fmt.Sprintf(
					"Timed out waiting for subagent %s after %d seconds (current status: %s)",
					taskID, timeoutSeconds, currentSubagentStatus(t.manager, taskID),
				))
			case <-doneCh:
				task, ok := t.manager.GetTask(taskID)
				if !ok {
					return ErrorResult(fmt.Sprintf("Subagent task disappeared: %s", taskID))
				}
				t.manager.ClaimResult(taskID)
				return SilentResult(formatSubagentTaskResult(task))
			}
		}
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrorResult(fmt.Sprintf("Wait for subagent %s interrupted", taskID))
		case <-timer.C:
			return ErrorResult(fmt.Sprintf(
				"Timed out waiting for subagent %s after %d seconds (current status: %s)",
				taskID, timeoutSeconds, currentSubagentStatus(t.manager, taskID),
			))
		case <-ticker.C:
			task, ok := t.manager.GetTask(taskID)
			if !ok {
				return ErrorResult(fmt.Sprintf("Subagent task disappeared: %s", taskID))
			}
			if isSubagentTerminal(task.Status) {
				t.manager.ClaimResult(taskID)
				return SilentResult(formatSubagentTaskResult(task))
			}
		}
	}
}

func currentSubagentStatus(manager *SubagentManager, taskID string) string {
	if task, ok := manager.GetTask(taskID); ok && task != nil {
		return task.Status
	}
	return "unknown"
}

func isSubagentTerminal(status string) bool {
	switch status {
	case SubagentStatusCompleted,
		SubagentStatusFailed,
		SubagentStatusNotDone,
		SubagentStatusCancelled,
		SubagentStatusNeedsContext:
		return true
	default:
		return false
	}
}

func formatSubagentTaskResult(task *SubagentTask) string {
	lines := []string{
		fmt.Sprintf("Subagent task %s finished.", task.ID),
		fmt.Sprintf("Status: %s", task.Status),
	}
	if task.Label != "" {
		lines = append(lines, fmt.Sprintf("Label: %s", task.Label))
	}
	if task.AgentID != "" {
		lines = append(lines, fmt.Sprintf("Agent: %s", task.AgentID))
	}
	if task.Summary != "" {
		lines = append(lines, fmt.Sprintf("Summary: %s", task.Summary))
	}
	if task.ContextRequest != "" {
		lines = append(lines, fmt.Sprintf("Context needed: %s", task.ContextRequest))
	}
	if task.Result != "" {
		lines = append(lines, "Details:\n"+task.Result)
	}

	switch task.Status {
	case SubagentStatusNeedsContext:
		lines = append(lines,
			fmt.Sprintf(
				"The subagent is paused waiting for guidance. Provide context and continue it with the spawn tool (task_id=%s, guidance=<context>).",
				task.ID,
			),
		)
	case SubagentStatusNotDone:
		lines = append(lines,
			"The subagent could not complete the task with the current constraints.",
		)
	case SubagentStatusCompleted:
		lines = append(lines,
			"The subagent finished successfully.",
		)
	}

	return strings.Join(lines, "\n")
}
