package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

var subagentFailureReason = map[providers.FailoverReason]string{
	providers.FailoverRateLimit:  "API rate limit exceeded",
	providers.FailoverTimeout:    "HTTP request timeout (server did not respond in time)",
	providers.FailoverOverloaded: "provider overloaded",
	providers.FailoverAuth:       "authentication failed",
	providers.FailoverBilling:    "provider billing issue",
	providers.FailoverFormat:     "request rejected as malformed",
	providers.FailoverUnknown:    "unexpected error during LLM call or tool execution",
}

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
	defaultThinkingLevel := sm.thinkingLevel
	sm.mu.RUnlock()

	maxIter = defaultMaxIter
	maxTokens := defaultMaxTokens
	temperature := defaultTemperature
	hasMaxTokens := defaultHasMaxTokens
	hasTemperature := defaultHasTemperature
	thinking := defaultThinkingLevel

	var agentWorkspace string
	var agentName string

	if getContextInfo != nil {
		ctxInfo := getContextInfo(agentID)
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
		thinking = ctxInfo.ThinkingLevel

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

	if model == "" {
		model = defaultModel
	}
	if provider == nil {
		provider = defaultManagerProvider
	}

	reasoningOpts := thinkingReasoningOptions(thinking)

	if hasMaxTokens || hasTemperature || reasoningOpts != nil {
		llmOptions = map[string]any{}
		if hasMaxTokens {
			llmOptions["max_tokens"] = maxTokens
		}
		if hasTemperature {
			llmOptions["temperature"] = temperature
		}
		if reasoningOpts != nil {
			llmOptions["reasoning"] = reasoningOpts
		}
	}

	return provider, model, systemPrompt, maxIter, llmOptions, contextWindow
}

func thinkingReasoningOptions(level string) map[string]any {
	normalized, ok := config.NormalizeThinkingLevel(level)
	if !ok {
		normalized = ""
	}
	switch normalized {
	case "":
		return nil
	case "off":
		return map[string]any{"enabled": false}
	default:
		return map[string]any{"effort": normalized, "enabled": true}
	}
}

func (sm *SubagentManager) ownerAgentID(task *SubagentTask) string {
	if task != nil && task.AgentID != "" {
		return task.AgentID
	}
	return sm.getDefaultAgentID()
}

func extractProgress(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), "progress:") {
			return strings.TrimSpace(trimmed[len("progress:"):])
		}
	}
	return ""
}

func isTransientFailure(err error) bool {
	if err == nil {
		return false
	}
	return providers.IsRetriableError(err)
}

func retryPendingLocked(task *SubagentTask) bool {
	return task.Status == SubagentStatusFailed &&
		task.MaxRetries > 0 &&
		task.RetryCount < task.MaxRetries &&
		isTransientFailure(task.lastErr)
}

func (sm *SubagentManager) runTask(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	defer func() {
		sm.mu.Lock()
		if c, ok := sm.cancels[task.ID]; ok {
			delete(sm.cancels, task.ID)
			sm.mu.Unlock()
			c()
		} else {
			sm.mu.Unlock()
		}
	}()

	for {
		sm.runTaskImpl(ctx, task, callback)

		sm.mu.Lock()
		if task.Status != SubagentStatusFailed || task.MaxRetries <= 0 ||
			task.RetryCount >= task.MaxRetries || !isTransientFailure(task.lastErr) {
			sm.mu.Unlock()
			sm.reportTerminalStatus(task)
			return
		}

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
				"error":       task.lastErr.Error(),
				"result":      task.Result,
			})

		task.Status = SubagentStatusRunning
		task.lastErr = nil
		task.Updated = time.Now().UnixMilli()
		sm.mu.Unlock()

		select {
		case <-ctx.Done():
			sm.mu.Lock()
			task.Status = SubagentStatusCancelled
			task.Summary = "Task cancelled during retry backoff"
			task.Result = "Task cancelled during retry backoff"
			task.Updated = time.Now().UnixMilli()
			sm.mu.Unlock()
			task.SignalDone()
			sm.reportTerminalStatus(task)
			return
		case <-retrySleep(backoff):
		}
	}
}

func (sm *SubagentManager) runTaskImpl(ctx context.Context, task *SubagentTask, callback AsyncCallback) {
	startTime := time.Now()

	sm.mu.Lock()
	previousTask := *task
	task.Status = SubagentStatusRunning
	task.lastErr = nil
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

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	agentProvider, agentModel, systemPrompt, maxIter, llmOptions, agentContextWindow := sm.resolveAgentConfig(task.AgentID)

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

	sm.mu.RLock()
	visionChecker := sm.visionChecker
	sm.mu.RUnlock()
	visionSupported := visionChecker != nil && visionChecker(agentModel)
	videoChecker := sm.getVideoChecker()
	videoSupported := videoChecker != nil && videoChecker(agentModel)

	ctx = WithVideoCaps(ctx, VideoCapabilities{Video: videoSupported, Vision: visionSupported})

	messages := previousTask.buildMessages(systemPrompt)

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

	sm.mu.RLock()
	tools := sm.tools
	recorder := sm.sessionRecorder
	sessionCompactor := sm.sessionCompactor
	compactionThreshold := sm.compactionThresholdPercent
	compactionModel := sm.compactionModel
	evictExcluded := sm.evictExcludedFromMemory
	tokenUsage := sm.tokenUsageReporter
	sm.mu.RUnlock()

	sessionKey := task.OriginSessionKey + ":" + task.ID

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

	loopOwner := subagentLoopOwner(task, sessionKey)

	billTokens := func(in, out int) {
		if tokenUsage == nil || loopOwner == "" {
			return
		}
		tokenUsage(loopOwner, in, out)
		if sessionKey != "" && sessionKey != loopOwner {
			tokenUsage(sessionKey, in, out)
		}
	}

	loopResult, err := RunToolLoop(ctx, ToolLoopConfig{
		Provider:                   agentProvider,
		Model:                      agentModel,
		Tools:                      tools,
		MaxIterations:              maxIter,
		LLMOptions:                 llmOptions,
		SessionRecorder:            recorder,
		SessionKey:                 sessionKey,
		OwnerAgentID:               sm.ownerAgentID(task),
		OwnerSessionKey:            loopOwner,
		Retry:                      retryConfigPtr(),
		ContextWindow:              agentContextWindow,
		CompactionThresholdPercent: compactionThreshold,
		MessageBus:                 sm.bus,
		ChatID:                     sessionKey,
		VisionSupported:            visionSupported,
		VideoSupported:             videoSupported,
		Redactor:                   sm.getRedactor(),

		CompactionModel:         compactionModel,
		SessionCompactor:        sessionCompactor,
		EvictExcludedFromMemory: evictExcluded,
		OnTokenUsage:            billTokens,
	}, messages, task.OriginChannel, task.OriginChatID)

	duration := time.Since(startTime)

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
		retryPending := retryPendingLocked(task)
		var cancel context.CancelFunc
		if !retryPending {
			if c, ok := sm.cancels[task.ID]; ok {
				cancel = c
				delete(sm.cancels, task.ID)
			}
		}
		sm.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if callback != nil && result != nil && !retryPending {
			callback(ctx, result)
		}
	}()

	if err != nil {
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
			reason := providers.FailoverUnknown
			if fe := providers.ClassifyError(err, "", ""); fe != nil {
				reason = fe.Reason
			}
			errType = string(reason)
			errDetails["reason"] = subagentFailureReason[reason]
			logger.ErrorCF("subagent", "Subagent FAILED: "+errType, errDetails)

			task.lastErr = err
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
		task.lastErr = nil
		task.Updated = time.Now().UnixMilli()

		if progress := extractProgress(loopResult.Content); progress != "" {
			task.Progress = progress
		}

		logger.InfoCF("subagent", "Subagent task finished",
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

}
