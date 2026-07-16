package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WaitForSubagentTool blocks until a previously spawned subagent reaches a
// terminal state (completed, failed, not_done, cancelled, or needs_context)
// or the specified timeout expires.
//
// This complements the async SpawnTool: spawn returns immediately and the
// result is delivered later via callback. WaitForSubagent lets the parent
// agent explicitly block and collect the result in the same tool-call turn.
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

func (t *WaitForSubagentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
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

	// Verify the task exists.
	task, ok := t.manager.GetTask(taskID)
	if !ok {
		return ErrorResult(fmt.Sprintf("Subagent task not found: %s", taskID))
	}

	// If already in a terminal/paused state, return immediately.
	if isSubagentTerminal(task.Status) {
		t.manager.MarkDelivered(taskID)
		return SilentResult(formatSubagentTaskResult(task))
	}

	// Poll until terminal, timeout, or context cancellation.
	timeout := time.Duration(timeoutSeconds) * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ErrorResult(fmt.Sprintf("Wait for subagent %s interrupted", taskID))
		case <-timer.C:
			// Final check in case the task completed between ticks.
			task, ok := t.manager.GetTask(taskID)
			if !ok {
				return ErrorResult(fmt.Sprintf("Subagent task disappeared: %s", taskID))
			}
			if isSubagentTerminal(task.Status) {
				t.manager.MarkDelivered(taskID)
				return SilentResult(formatSubagentTaskResult(task))
			}
			return ErrorResult(fmt.Sprintf(
				"Timed out waiting for subagent %s after %d seconds (current status: %s)",
				taskID, timeoutSeconds, task.Status,
			))
		case <-ticker.C:
			task, ok := t.manager.GetTask(taskID)
			if !ok {
				return ErrorResult(fmt.Sprintf("Subagent task disappeared: %s", taskID))
			}
			if isSubagentTerminal(task.Status) {
				t.manager.MarkDelivered(taskID)
				return SilentResult(formatSubagentTaskResult(task))
			}
		}
	}
}

// isSubagentTerminal returns true for all statuses where the subagent is no
// longer actively executing (completed, failed, not_done, cancelled, or
// paused waiting for context).
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

// formatSubagentTaskResult builds a human-readable summary of a subagent
// task's final state for the LLM.
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
