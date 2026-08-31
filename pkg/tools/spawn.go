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
			"model": map[string]interface{}{
				"type":        "string",
				"description": "Optional model to use for the subagent (e.g., 'anthropic:claude-opus'). Defaults to the agent's configured model.",
			},
			"dependencies": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Optional list of subagent task IDs that must complete before this task starts",
			},
			"max_retries": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum automatic retry attempts for transient failures. Omitted or non-positive values fall back to the configured default (agents.defaults.subagent_max_retries, default 2).",
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
	modelOverride, _ := args["model"].(string)

	// Extract dependencies
	var dependencies []string
	if depsRaw, ok := args["dependencies"].([]interface{}); ok {
		for _, d := range depsRaw {
			if depStr, ok := d.(string); ok && strings.TrimSpace(depStr) != "" {
				dependencies = append(dependencies, strings.TrimSpace(depStr))
			}
		}
	}

	// Extract max_retries
	maxRetries := 0
	if mr, ok := args["max_retries"].(float64); ok {
		maxRetries = int(mr)
	}

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

	originChannel := t.originChannel
	originChatID := t.originChatID
	if ch, cid := ToolContextFromCtx(ctx); ch != "" {
		originChannel = ch
		originChatID = cid
	}

	var (
		result string
		err    error
	)

	if strings.TrimSpace(taskID) != "" {
		result, err = t.manager.ContinueTask(ctx, taskID, guidance, t.callback)
	} else {
		result, err = t.manager.SpawnWithOptions(ctx, task, label, agentID, originChannel, originChatID, t.callback, SpawnOptions{
			Dependencies:  dependencies,
			MaxRetries:    maxRetries,
			ModelOverride: modelOverride,
		})
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

// ExtractSpawnTaskID extracts the raw task ID (e.g. "subagent-3") from the
// human-readable message returned by SpawnWithOptions
// (e.g. "Spawned subagent task subagent-3 ('...') for task: ...").
// Returns an empty string if no task ID is found.
func ExtractSpawnTaskID(result string) string {
	return extractSpawnTaskID(result)
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
