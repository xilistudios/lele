package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	startTime := time.Now()

	sm.mu.Lock()
	previousTask := *task
	task.Status = SubagentStatusRunning
	task.Updated = time.Now().UnixMilli()
	timeout := sm.timeout
	sm.mu.Unlock()

	logger.InfoCF("subagent", "Subagent task started",
		map[string]interface{}{
			"task_id":        task.ID,
			"label":          task.Label,
			"agent_id":       task.AgentID,
			"origin_channel": task.OriginChannel,
			"origin_chat_id": task.OriginChatID,
			"timeout":        timeout.String(),
			"task_preview":   truncateString(task.Task, 200),
		})

	// Apply timeout to the context if configured.
	// This creates a child context that will be cancelled after the timeout,
	// while the parent cancel func (stored in sm.cancels) can still cancel it earlier.
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Get the specific agent's context info (AGENT.md, SOUL.md, workspace, name, model, provider from its workspace)
	sm.mu.RLock()
	getContextInfo := sm.getAgentContext
	agentID := task.AgentID
	sm.mu.RUnlock()

	// Build system prompt for subagent using its own context
	var systemPrompt string
	var agentWorkspace string
	var agentName string
	var agentModel string
	var agentProvider providers.LLMProvider
	var agentMaxIterations int
	var agentMaxTokens int
	var agentTemperature float64
	var agentContextWindow int

	if getContextInfo != nil {
		ctxInfo := getContextInfo(agentID)
		// Always use the agent's model and provider from its config, even if context is empty.
		agentModel = ctxInfo.Model
		agentProvider = ctxInfo.Provider
		agentMaxIterations = ctxInfo.MaxIterations
		agentMaxTokens = ctxInfo.MaxTokens
		agentTemperature = ctxInfo.Temperature
		agentContextWindow = ctxInfo.ContextWindow
		if ctxInfo.Context != "" {
			agentWorkspace = ctxInfo.Workspace
			agentName = ctxInfo.Name
			if agentName == "" {
				agentName = agentID
			}
			systemPrompt = buildSubagentSystemPrompt(ctxInfo.Context, agentID, agentName, agentWorkspace)
		}
	}

	if systemPrompt == "" {
		systemPrompt = buildSubagentSystemPrompt("", agentID, agentID, "")
	}

	// Use the agent's model and provider if available, otherwise fall back to manager's defaults
	if agentModel == "" {
		agentModel = sm.defaultModel
	}
	if agentProvider == nil {
		agentProvider = sm.provider
	}

	messages := previousTask.buildMessages(systemPrompt)

	// Check if context is already cancelled before starting
	select {
	case <-ctx.Done():
		sm.mu.Lock()
		task.Status = SubagentStatusCancelled
		task.Summary = "Task cancelled before execution"
		task.Result = "Task cancelled before execution"
		task.Updated = time.Now().UnixMilli()
		sm.mu.Unlock()
		return
	default:
	}

	// Run tool loop with access to tools.
	// Use target agent's MaxIterations/MaxTokens/Temperature if available,
	// otherwise fall back to the SubagentManager's defaults.
	sm.mu.RLock()
	tools := sm.tools
	maxIter := sm.maxIterations
	maxTokens := sm.maxTokens
	temperature := sm.temperature
	hasMaxTokens := sm.hasMaxTokens
	hasTemperature := sm.hasTemperature
	recorder := sm.sessionRecorder
	sm.mu.RUnlock()

	// Target agent overrides take precedence over SubagentManager defaults
	if agentMaxIterations > 0 {
		maxIter = agentMaxIterations
	}
	if agentMaxTokens > 0 {
		maxTokens = agentMaxTokens
		hasMaxTokens = true
	}
	if agentTemperature > 0 {
		temperature = agentTemperature
		hasTemperature = true
	}

	// Build subagent session key: {origin_session_key}:{task_id}
	// This ensures subagent history is saved alongside the parent session
	sessionKey := task.OriginSessionKey + ":" + task.ID

	// Notify the session key callback so the owner can build an O(1) lookup map
	sm.mu.RLock()
	cb := sm.sessionKeyCallback
	registerCancel := sm.registerSessionCancel
	sm.mu.RUnlock()
	if cb != nil {
		cb(sessionKey, agentID)
	}

	if registerCancel != nil {
		cleanup := registerCancel(sessionKey, func() {
			sm.mu.Lock()
			cancelFn, ok := sm.cancels[task.ID]
			sm.mu.Unlock()
			if ok && cancelFn != nil {
				cancelFn()
			}
		})
		defer cleanup()
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

	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:        agentProvider,
		Model:           agentModel,
		Tools:           tools,
		MaxIterations:   maxIter,
		LLMOptions:      llmOptions,
		SessionRecorder: recorder,
		SessionKey:      sessionKey,
		Retry:           retryConfigPtr(),
		ContextWindow:   agentContextWindow,
		MessageBus:      sm.bus,
		ChatID:          sessionKey,
	}, messages, task.OriginChannel, task.OriginChatID)

	duration := time.Since(startTime)

	// Save subagent history to disk if recorder is available
	if recorder != nil && sessionKey != "" {
		if err := recorder.Save(sessionKey); err != nil {
			logger.ErrorCF("subagent", "Failed to save subagent history", map[string]interface{}{
				"session_key": sessionKey,
				"task_id":     task.ID,
				"error":       err.Error(),
			})
		}
	}

	sm.mu.Lock()
	var result *ToolResult
	defer func() {
		var cancel context.CancelFunc
		if c, ok := sm.cancels[task.ID]; ok {
			cancel = c
			delete(sm.cancels, task.ID)
		}
		sm.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		// Call callback if provided and result is set
		if callback != nil && result != nil {
			callback(ctx, result)
		}
	}()

	if err != nil {
		// Classify the error for detailed logging
		errType := "unknown"
		errDetails := map[string]interface{}{
			"task_id":        task.ID,
			"label":          task.Label,
			"agent_id":       task.AgentID,
			"model":          agentModel,
			"origin_channel": task.OriginChannel,
			"origin_chat_id": task.OriginChatID,
			"duration":       duration.String(),
			"duration_ms":    duration.Milliseconds(),
			"error":          err.Error(),
			"error_type":     fmt.Sprintf("%T", err),
		}

		if ctx.Err() == context.DeadlineExceeded {
			errType = "timeout"
			errDetails["reason"] = "context deadline exceeded (subagent timeout)"
			errDetails["timeout_config"] = timeout.String()
			logger.ErrorCF("subagent", "Subagent FAILED: timeout exceeded",
				errDetails)

			task.Status = SubagentStatusFailed
			task.Summary = "Subagent timed out"
			task.Result = fmt.Sprintf("The subagent exceeded its time limit (%s) and was stopped. Error: %v", timeout, err)
		} else if ctx.Err() == context.Canceled {
			errType = "cancelled"
			errDetails["reason"] = "context cancelled (manual stop or parent cancellation)"
			logger.WarnCF("subagent", "Subagent CANCELLED: context was cancelled",
				errDetails)

			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled during execution"
			task.Result = "Task cancelled during execution"
		} else {
			// Check for specific error patterns in the error message
			errMsg := strings.ToLower(err.Error())
			switch {
			case strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded"):
				errType = "http_timeout"
				errDetails["reason"] = "HTTP request timeout (server did not respond in time)"
				logger.ErrorCF("subagent", "Subagent FAILED: HTTP timeout - server did not respond",
					errDetails)
			case strings.Contains(errMsg, "rate limit") || strings.Contains(errMsg, "429"):
				errType = "rate_limited"
				errDetails["reason"] = "API rate limit exceeded"
				logger.ErrorCF("subagent", "Subagent FAILED: rate limited by API",
					errDetails)
			case strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "no such host"):
				errType = "connection_error"
				errDetails["reason"] = "cannot connect to LLM API endpoint"
				logger.ErrorCF("subagent", "Subagent FAILED: connection error to LLM API",
					errDetails)
			case strings.Contains(errMsg, "500") || strings.Contains(errMsg, "502") || strings.Contains(errMsg, "503"):
				errType = "server_error"
				errDetails["reason"] = "LLM API server error (5xx)"
				logger.ErrorCF("subagent", "Subagent FAILED: LLM API server error",
					errDetails)
			default:
				errDetails["reason"] = "unexpected error during LLM call or tool execution"
				logger.ErrorCF("subagent", "Subagent FAILED: unexpected error",
					errDetails)
			}
		}

		task.Status = SubagentStatusFailed
		task.Summary = fmt.Sprintf("Subagent execution failed [%s]", errType)
		task.Result = fmt.Sprintf("Error [%s]: %v", errType, err)
		task.ContextRequest = ""
		task.Updated = time.Now().UnixMilli()
		result = &ToolResult{
			ForLLM:   task.statusMessage(),
			Silent:   true,
			IsError:  true,
			Async:    false,
			Err:      err,
			Metadata: map[string]string{"task_id": task.ID, "subagent_session_key": sessionKey},
		}
	} else {
		outcome := parseSubagentOutcome(loopResult.Content)
		task.Status = outcome.Status
		task.Summary = outcome.Summary
		task.Result = outcome.Details
		task.ContextRequest = outcome.ContextRequest
		task.Iterations = loopResult.Iterations
		task.Updated = time.Now().UnixMilli()

		logger.InfoCF("subagent", "Subagent task completed",
			map[string]interface{}{
				"task_id":     task.ID,
				"label":       task.Label,
				"agent_id":    task.AgentID,
				"model":       agentModel,
				"status":      task.Status,
				"iterations":  loopResult.Iterations,
				"duration":    duration.String(),
				"duration_ms": duration.Milliseconds(),
				"summary":     task.Summary,
			})

		result = &ToolResult{
			ForLLM:   task.statusMessage(),
			Silent:   true,
			IsError:  false,
			Async:    false,
			Metadata: map[string]string{"task_id": task.ID, "subagent_session_key": sessionKey},
		}
	}

	// NOTE: Subagents do NOT send messages directly to users.
	// Their output is returned as LLM context for the parent agent only.
}
