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

	if t.manager == nil {
		return ErrorResult("Subagent manager not configured").WithError(fmt.Errorf("manager is nil"))
	}

	// Build messages for subagent
	messages := []providers.Message{
		{
			Role:    "system",
			Content: "You are a subagent. Complete the given task independently and provide a clear, concise result.",
		},
		{
			Role:    "user",
			Content: task,
		},
	}

	// Use RunToolLoop to execute with tools (same as async SpawnTool)
	sm := t.manager
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	maxTokens := sm.maxTokens
	temperature := sm.temperature
	hasMaxTokens := sm.hasMaxTokens
	hasTemperature := sm.hasTemperature
	getContextInfo := sm.getAgentContext
	sm.mu.RUnlock()

	// Resolve provider and model from the agent's config via callback.
	// This ensures the subagent uses the same model/provider as the parent agent,
	// not the manager's defaults.
	agentProvider := sm.provider
	agentModel := sm.defaultModel
	if getContextInfo != nil {
		// Pass empty agentID to use the parent agent's config (fallback behavior)
		ctxInfo := getContextInfo("")
		if ctxInfo.Model != "" {
			agentModel = ctxInfo.Model
		}
		if ctxInfo.Provider != nil {
			agentProvider = ctxInfo.Provider
		}
		// Use target agent's settings when available
		if ctxInfo.MaxIterations > 0 {
			maxIter = ctxInfo.MaxIterations
		}
		if ctxInfo.MaxTokens > 0 {
			maxTokens = ctxInfo.MaxTokens
			hasMaxTokens = true
		}
		if ctxInfo.Temperature > 0 {
			temperature = ctxInfo.Temperature
			hasTemperature = true
		}
	}

	var llmOptions map[string]any
	if hasMaxTokens || hasTemperature {
		llmOptions = map[string]any{}
		if hasMaxTokens {
			llmOptions["max_tokens"] = maxTokens
		}
		if hasTemperature {
			llmOptions["temperature"] = temperature
		}
	}

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

	// ForLLM: Full execution details
	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	llmContent := fmt.Sprintf("Subagent task completed:\nLabel: %s\nIterations: %d\nResult: %s",
		labelStr, loopResult.Iterations, loopResult.Content)

	return &ToolResult{
		ForLLM:  llmContent,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}
