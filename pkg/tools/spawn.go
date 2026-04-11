package tools

import (
	"context"
	"fmt"
	"strings"
)

type SpawnTool struct {
	manager        *SubagentManager
	originChannel  string
	originChatID   string
	allowlistCheck func(targetAgentID string) bool
	callback       AsyncCallback // For async completion notification
}

func NewSpawnTool(manager *SubagentManager) *SpawnTool {
	return &SpawnTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

// SetCallback implements AsyncTool interface for async completion notification
func (t *SpawnTool) SetCallback(cb AsyncCallback) {
	t.callback = cb
}

func (t *SpawnTool) Name() string {
	return "spawn"
}

func (t *SpawnTool) Description() string {
	return "Spawn a subagent to handle a task in the background, or continue a paused subagent that is waiting for context. The subagent reports its status back to the parent agent instead of messaging users directly."
}

func (t *SpawnTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional existing subagent task ID to continue",
			},
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task for a new subagent to complete",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"guidance": map[string]interface{}{
				"type":        "string",
				"description": "Additional guidance when continuing a paused subagent",
			},
			"agent_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional target agent ID to delegate the task to",
			},
		},
	}
}

func (t *SpawnTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *SpawnTool) SetAllowlistChecker(check func(targetAgentID string) bool) {
	t.allowlistCheck = check
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	task, _ := args["task"].(string)
	label, _ := args["label"].(string)
	taskID, _ := args["task_id"].(string)
	guidance, _ := args["guidance"].(string)
	agentID, _ := args["agent_id"].(string)

	if strings.TrimSpace(taskID) == "" && strings.TrimSpace(task) == "" {
		return ErrorResult("task is required when task_id is not provided")
	}

	// Check allowlist if targeting a specific agent
	if agentID != "" && t.allowlistCheck != nil {
		if !t.allowlistCheck(agentID) {
			return ErrorResult(fmt.Sprintf("not allowed to spawn agent '%s'", agentID))
		}
	}

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured")
	}

	var (
		result string
		err    error
	)

	if strings.TrimSpace(taskID) != "" {
		result, err = t.manager.ContinueTask(ctx, taskID, guidance, t.callback)
	} else {
		result, err = t.manager.Spawn(ctx, task, label, agentID, t.originChannel, t.originChatID, t.callback)
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to manage subagent: %v", err))
	}

	toolResult := AsyncResult(result)
	if taskID != "" {
		if toolResult.Metadata == nil {
			toolResult.Metadata = map[string]string{}
		}
		toolResult.Metadata["task_id"] = taskID
		toolResult.Metadata["subagent_session_key"] = "subagent:" + taskID
		return toolResult
	}

	if extractedTaskID := extractSpawnTaskID(result); extractedTaskID != "" {
		if toolResult.Metadata == nil {
			toolResult.Metadata = map[string]string{}
		}
		toolResult.Metadata["task_id"] = extractedTaskID
		toolResult.Metadata["subagent_session_key"] = "subagent:" + extractedTaskID
	}

	return toolResult
}

func extractSpawnTaskID(result string) string {
	idx := strings.Index(result, "subagent-")
	if idx < 0 {
		return ""
	}
	trimmed := result[idx:]
	if end := strings.IndexAny(trimmed, " \t\n('\""); end > 0 {
		return trimmed[:end]
	}
	return trimmed
}
