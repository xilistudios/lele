package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// resolveAgentConfig resolves the agent's provider, model, system prompt, max iterations,
// LLM options, and context window from the SubagentManager's configuration and the
// target agent's context callback. Both runTask and SubagentTool use this method
// to ensure consistent provider/model/system-prompt resolution.
func (sm *SubagentManager) resolveAgentConfig(agentID string) (
	provider providers.LLMProvider,
	model string,
	systemPrompt string,
	maxIter int,
	llmOptions map[string]any,
	contextWindow int,
) {
	sm.mu.RLock()
	getContextInfo := sm.getAgentContext
	defaultModel := sm.defaultModel
	defaultManagerProvider := sm.provider
	defaultMaxIter := sm.maxIterations
	defaultMaxTokens := sm.maxTokens
	defaultTemperature := sm.temperature
	defaultHasMaxTokens := sm.hasMaxTokens
	defaultHasTemperature := sm.hasTemperature
	sm.mu.RUnlock()

	// Start with manager defaults
	maxIter = defaultMaxIter
	maxTokens := defaultMaxTokens
	temperature := defaultTemperature
	hasMaxTokens := defaultHasMaxTokens
	hasTemperature := defaultHasTemperature

	var agentWorkspace string
	var agentName string

	if getContextInfo != nil {
		ctxInfo := getContextInfo(agentID)
		// Always use the agent's model and provider from its config, even if context is empty.
		model = ctxInfo.Model
		provider = ctxInfo.Provider
		contextWindow = ctxInfo.ContextWindow

		logger.DebugCF("subagent", "resolveAgentConfig: agent context resolved",
			map[string]interface{}{
				"agent_id":       agentID,
				"model":          model,
				"provider_type":  fmt.Sprintf("%T", provider),
				"context_window": contextWindow,
			})

		// Agent overrides take precedence over SubagentManager defaults
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
	if model == "" {
		model = defaultModel
	}
	if provider == nil {
		provider = defaultManagerProvider
	}

	// Build LLM options
	if hasMaxTokens || hasTemperature {
		llmOptions = map[string]any{}
		if hasMaxTokens {
			llmOptions["max_tokens"] = maxTokens
		}
		if hasTemperature {
			llmOptions["temperature"] = temperature
		}
	}

	return provider, model, systemPrompt, maxIter, llmOptions, contextWindow
}

// extractProgress extracts a "PROGRESS:" line from raw subagent output.
// Subagents can include a "PROGRESS: <message>" line in their response to report
// intermediate status updates before the final STATUS/SUMMARY/DETAILS block.
func extractProgress(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "progress:") {
			return strings.TrimSpace(trimmed[len("progress:"):])
		}
	}
	return ""
}

// isTransientFailure checks if a task's result string indicates a transient failure
// that may be worth retrying.
func isTransientFailure(result string) bool {
	lower := strings.ToLower(result)
	return strings.Contains(lower, "rate_limited") ||
		strings.Contains(lower, "connection_error") ||
		strings.Contains(lower, "http_timeout") ||
		strings.Contains(lower, "server_error")
}

// runTask is the public entry point for running a subagent task.
// It wraps runTaskImpl with retry logic for transient failures.
func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	sm.runTaskImpl(ctx, task, callback)

	// After runTaskImpl completes, check if we should retry
	sm.mu.Lock()
	if task.Status == SubagentStatusFailed && task.MaxRetries > 0 && task.RetryCount < task.MaxRetries && isTransientFailure(task.Result) {
		task.RetryCount++
		backoff := time.Duration(task.RetryCount) * 5 * time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}

		logger.InfoCF("subagent", "Retrying subagent task after transient failure",
			map[string]interface{}{
				"task_id":     task.ID,
				"retry_count": task.RetryCount,
				"max_retries": task.MaxRetries,
				"backoff":     backoff.String(),
				"result":      task.Result,
			})

		task.Status = SubagentStatusRunning
		task.Updated = time.Now().UnixMilli()
		sm.mu.Unlock()

		// Wait for backoff, but respect context cancellation
		select {
		case <-ctx.Done():
			sm.mu.Lock()
			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled during retry backoff"
			task.Result = "Task cancelled during retry backoff"
			task.Updated = time.Now().UnixMilli()
			sm.mu.Unlock()
			task.SignalDone()
			return
		case <-time.After(backoff):
		}

		// Recursive retry
		sm.runTask(ctx, task, callback)
		return
	}
	sm.mu.Unlock()
}

// runTaskImpl contains the actual subagent execution logic.
// It runs the tool loop, parses the outcome, and updates task state.
// Retry logic is handled by the runTask wrapper.
func (sm *SubagentManager) runTaskImpl(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
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

	// Resolve agent config using shared method
	agentProvider, agentModel, systemPrompt, maxIter, llmOptions, agentContextWindow := sm.resolveAgentConfig(task.AgentID)

	// Apply per-task model override if configured. The override is a hard
	// requirement: if it cannot be resolved, the task FAILS loudly instead of
	// silently running on the agent's default model (which would ignore the
	// model the user selected, e.g. via a cron spawn job).
	if task.ModelOverride != "" {
		sm.mu.RLock()
		resolver := sm.modelOverrideResolver
		sm.mu.RUnlock()

		var reason string
		var overrideProvider providers.LLMProvider
		var overrideModel string
		var overrideWindow int
		if resolver == nil {
			reason = fmt.Sprintf("no model resolver configured to honor model override %q", task.ModelOverride)
		} else {
			resolverErr := error(nil)
			overrideProvider, overrideModel, overrideWindow, resolverErr = resolver(task.ModelOverride)
			switch {
			case resolverErr != nil:
				reason = resolverErr.Error()
			case overrideProvider == nil || overrideModel == "":
				reason = "resolver returned no provider"
			}
		}

		if reason != "" {
			msg := fmt.Sprintf("subagent model override %q could not be applied: %s", task.ModelOverride, reason)
			logger.ErrorCF("subagent", "Subagent model override could not be applied, failing task",
				map[string]interface{}{
					"task_id":        task.ID,
					"agent_id":       task.AgentID,
					"model_override": task.ModelOverride,
					"error":          reason,
				})

			// Mirror the terminal-state mutation style of the early-cancel path
			// below, then deliver the failure through the cancels cleanup +
			// callback exactly like the deferred block at the end of this
			// function does, so the callback fires exactly once and no cancel
			// func leaks. runTask (the wrapper) sees Status==Failed with a
			// non-transient Result and will not retry; the task goroutine calls
			// SignalDone after runTask returns.
			sm.mu.Lock()
			task.Status = SubagentStatusFailed
			task.Summary = msg
			task.Result = msg
			task.Updated = time.Now().UnixMilli()
			var cancel context.CancelFunc
			if c, ok := sm.cancels[task.ID]; ok {
				cancel = c
				delete(sm.cancels, task.ID)
			}
			sm.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if callback != nil {
				callback(ctx, &ToolResult{
					ForLLM:   msg,
					Silent:   true,
					IsError:  true,
					Async:    false,
					Metadata: map[string]string{"task_id": task.ID, "subagent_session_key": task.OriginSessionKey + ":" + task.ID},
				})
			}
			return
		}

		agentProvider = overrideProvider
		agentModel = overrideModel
		if overrideWindow > 0 {
			agentContextWindow = overrideWindow
		}
		logger.InfoCF("subagent", "Subagent using model override",
			map[string]interface{}{
				"task_id":        task.ID,
				"agent_id":       task.AgentID,
				"model_override": task.ModelOverride,
				"resolved_model": overrideModel,
			})
	}

	// Determine whether the resolved model supports vision so RunToolLoop
	// can filter out read_image for non-vision models.
	sm.mu.RLock()
	visionChecker := sm.visionChecker
	sm.mu.RUnlock()
	visionSupported := visionChecker != nil && visionChecker(agentModel)

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

	// Get tools and session recorder
	sm.mu.RLock()
	tools := sm.tools
	recorder := sm.sessionRecorder
	sessionCompactor := sm.sessionCompactor
	compactionThreshold := sm.compactionThresholdPercent
	compactionModel := sm.compactionModel
	evictExcluded := sm.evictExcludedFromMemory
	sm.mu.RUnlock()

	// Build subagent session key: {origin_session_key}:{task_id}
	// This ensures subagent history is saved alongside the parent session
	sessionKey := task.OriginSessionKey + ":" + task.ID

	// Notify the session key callback so the owner can build an O(1) lookup map
	sm.mu.RLock()
	cb := sm.sessionKeyCallback
	registerCancel := sm.registerSessionCancel
	sm.mu.RUnlock()
	if cb != nil {
		cb(sessionKey, task.AgentID)
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

	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:                   agentProvider,
		Model:                      agentModel,
		Tools:                      tools,
		MaxIterations:              maxIter,
		LLMOptions:                 llmOptions,
		SessionRecorder:            recorder,
		SessionKey:                 sessionKey,
		Retry:                      retryConfigPtr(),
		ContextWindow:              agentContextWindow,
		CompactionThresholdPercent: compactionThreshold,
		MessageBus:                 sm.bus,
		ChatID:                     sessionKey,
		VisionSupported:            visionSupported,
		Redactor:                   sm.getRedactor(),

		CompactionModel:         compactionModel,
		SessionCompactor:        sessionCompactor,
		EvictExcludedFromMemory: evictExcluded,
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

		// Extract intermediate progress from the subagent output
		if progress := extractProgress(loopResult.Content); progress != "" {
			task.Progress = progress
		}

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
