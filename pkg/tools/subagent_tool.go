package tools

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
)

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

	agentProvider, agentModel, systemPrompt, maxIter, llmOptions, agentContextWindow := sm.resolveAgentConfig(agentID)

	sm.mu.RLock()
	visionChecker := sm.visionChecker
	sm.mu.RUnlock()
	visionSupported := visionChecker != nil && visionChecker(agentModel)
	videoChecker := sm.getVideoChecker()
	videoSupported := videoChecker != nil && videoChecker(agentModel)

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

	sm.mu.RLock()
	tools := sm.tools
	sm.mu.RUnlock()

	originChannel := t.originChannel
	originChatID := t.originChatID
	if ch, cid := ToolContextFromCtx(ctx); ch != "" {
		originChannel = ch
		originChatID = cid
	}

	ownerAgentID, ownerSessionKey := AgentToolContextFromCtx(ctx)

	sm.mu.RLock()
	tokenUsage := sm.tokenUsageReporter
	sm.mu.RUnlock()
	billTokens := func(in, out int) {
		if tokenUsage == nil || ownerSessionKey == "" {
			return
		}
		tokenUsage(ownerSessionKey, in, out)
	}

	taskCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sm.mu.RLock()
	timeout := sm.timeout
	sm.mu.RUnlock()
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		taskCtx, timeoutCancel = context.WithTimeout(taskCtx, timeout)
		defer timeoutCancel()
	}

	taskCtx = WithVideoCaps(taskCtx, VideoCapabilities{Video: videoSupported, Vision: visionSupported})

	loopResult, err := RunToolLoop(taskCtx, ToolLoopConfig{
		Provider:                   agentProvider,
		Model:                      agentModel,
		Tools:                      tools,
		MaxIterations:              maxIter,
		LLMOptions:                 llmOptions,
		Retry:                      retryConfigPtr(),
		VisionSupported:            visionSupported,
		VideoSupported:             videoSupported,
		ContextWindow:              agentContextWindow,
		CompactionThresholdPercent: sm.getCompactionThresholdPercent(),
		Redactor:                   t.manager.getRedactor(),
		OwnerAgentID:               ownerAgentID,
		OwnerSessionKey:            ownerSessionKey,
		OnTokenUsage:               billTokens,
	}, messages, originChannel, originChatID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Subagent execution failed: %v", err)).WithError(err)
	}

	outcome := parseSubagentOutcome(loopResult.Content)

	labelStr := label
	if labelStr == "" {
		if agentID != "" {
			labelStr = agentID
		} else {
			labelStr = "(unnamed)"
		}
	}

	llmContent := fmt.Sprintf("%s\nLabel: %s\nAgent: %s\nIterations: %d\nStatus: %s\nSummary: %s\nDetails:\n%s",
		subagentInlineResultHeadline(outcome.Status),
		labelStr, agentID, loopResult.Iterations, outcome.Status, outcome.Summary, outcome.Details)

	return &ToolResult{
		ForLLM:  llmContent,
		Silent:  true,
		IsError: false,
		Async:   false,
	}
}

func subagentInlineResultHeadline(status string) string {
	switch status {
	case SubagentStatusCompleted:
		return "Subagent task completed:"
	case SubagentStatusNotDone:
		return "Subagent task finished but did NOT complete:"
	case SubagentStatusNeedsContext:
		return "Subagent paused and needs context:"
	case SubagentStatusCancelled:
		return "Subagent task was cancelled:"
	case SubagentStatusFailed:
		return "Subagent task failed:"
	default:
		return "Subagent task finished:"
	}
}
