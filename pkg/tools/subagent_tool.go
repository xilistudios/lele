package tools

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
)

// SubagentTool executes a subagent task synchronously and returns the result.
// Unlike SpawnTool which runs tasks asynchronously, SubagentTool waits for completion
// and returns the result directly in the ToolResult.
type SubagentTool struct {
	manager       *SubagentManager
	originChannel string
	originChatID  string
}

func NewSubagentTool(manager *SubagentManager) *SubagentTool {
	return &SubagentTool{
		manager:       manager,
		originChannel: "cli",
		originChatID:  "direct",
	}
}

func (t *SubagentTool) Name() string {
	return "subagent"
}

func (t *SubagentTool) Description() string {
	return "Execute a subagent task synchronously and return the result to the parent agent. Use this for delegating specific tasks to an independent agent instance."
}

func (t *SubagentTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"agent_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional target agent ID to delegate the task to",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SubagentTool) SetContext(channel, chatID string) {
	t.originChannel = channel
	t.originChatID = chatID
}

func (t *SubagentTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	task, ok := args["task"].(string)
	if !ok {
		return ErrorResult("task is required").WithError(fmt.Errorf("task parameter is required"))
	}

	label, _ := args["label"].(string)
	agentID, _ := args["agent_id"].(string)

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured").WithError(fmt.Errorf("manager is nil"))
	}

	sm := t.manager

	// Resolve agent config using shared method (same as runTask)
	agentProvider, agentModel, systemPrompt, maxIter, llmOptions, _ := sm.resolveAgentConfig(agentID)

	// Build messages the same way as runTask: system prompt + user task
	messages := []providers.Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
		{
			Role:    "user",
			Content: task,
		},
	}

	// Get tools from manager
	sm.mu.RLock()
	tools := sm.tools
	sm.mu.RUnlock()

	// Resolve origin channel/chatID from context if available
	originChannel := t.originChannel
	originChatID := t.originChatID
	if ch, cid := ToolContextFromCtx(ctx); ch != "" {
		originChannel = ch
		originChatID = cid
	}

	// Use a background context to decouple the subagent from the parent agent's
	// lifecycle. This prevents the subagent from being killed by parent context
	// cancellation (e.g., timeouts, /stop commands). The subagent should run
	// independently like its async counterpart (SpawnTool).
	taskCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Apply timeout if configured
	sm.mu.RLock()
	timeout := sm.timeout
	sm.mu.RUnlock()
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		taskCtx, timeoutCancel = context.WithTimeout(taskCtx, timeout)
		defer timeoutCancel()
	}

	loopResult, err := RunToolLoop(taskCtx, ToolLoopConfig{
		Provider:      agentProvider,
		Model:         agentModel,
		Tools:         tools,
		MaxIterations: maxIter,
		LLMOptions:    llmOptions,
		Retry:         retryConfigPtr(),
	}, messages, originChannel, originChatID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Subagent execution failed: %v", err)).WithError(err)
	}

	// Parse the outcome using the same parser as runTask
	outcome := parseSubagentOutcome(loopResult.Content)

	// Build the label string for display
	labelStr := label
	if labelStr == "" {
		if agentID != "" {
			labelStr = agentID
		} else {
			labelStr = "(unnamed)"
		}
	}

	llmContent := fmt.Sprintf("Subagent task completed:\nLabel: %s\nAgent: %s\nIterations: %d\nStatus: %s\nSummary: %s\nDetails:\n%s",
		labelStr, agentID, loopResult.Iterations, outcome.Status, outcome.Summary, outcome.Details)

	return &ToolResult{
		ForLLM:  llmContent,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}
