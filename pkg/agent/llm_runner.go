package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

type llmRunner interface {
	runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error)
	maybeRunGoalContinuation(agent *AgentInstance, opts processOptions, lastResponse string)
}

const transientLLMRetries = 3

func transientLLMBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
}

const maxConsecutiveEmptyResponses = 5

func emptyRetryBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
}

type llmRunnerImpl struct {
	al *AgentLoop

	retryWait func(time.Duration) <-chan time.Time

	loopTimeoutUnit time.Duration
}

func newLLMRunner(al *AgentLoop) *llmRunnerImpl {
	return &llmRunnerImpl{al: al, loopTimeoutUnit: time.Minute}
}

func (lr *llmRunnerImpl) waitForBackoff(ctx context.Context, d time.Duration) bool {
	var ch <-chan time.Time
	if lr.retryWait != nil {
		ch = lr.retryWait(d)
	} else {
		ch = time.After(d)
	}
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

func (lr *llmRunnerImpl) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error) {
	if opts.SessionKey != "" {
		sem, _ := lr.al.sessionProcessing.LoadOrStore(opts.SessionKey, make(chan struct{}, 1))
		semCh := sem.(chan struct{})
		select {
		case semCh <- struct{}{}:
			defer func() { <-semCh }()
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	noticeQueue := lr.al.activateMidTurnNotices(opts.SessionKey)
	defer func() {
		for _, n := range noticeQueue.deactivate() {
			if !lr.al.bus.PublishInbound(bus.InboundMessage{
				Channel:    "system",
				SenderID:   midTurnNoticeSender,
				ChatID:     fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID),
				Content:    n.content,
				SessionKey: opts.SessionKey,
				Metadata:   map[string]string{"task_id": n.taskID},
			}) {
				logger.ErrorCF("agent", "Mid-turn subagent notice lost: inbound bus rejected publish", map[string]interface{}{
					"session_key": opts.SessionKey,
					"task_id":     n.taskID,
				})
			}
		}
	}()

	if opts.Channel != "" && opts.ChatID != "" {
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := lr.al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	runCtx := ctx
	if opts.SessionKey != "" {
		sessionCtx, cancel := context.WithCancel(ctx)
		var timeoutCancel context.CancelFunc
		if agent.LLMLoopTimeoutMinutes > 0 {
			unit := lr.loopTimeoutUnit
			if unit <= 0 {
				unit = time.Minute
			}
			runCtx, timeoutCancel = context.WithTimeout(sessionCtx, time.Duration(agent.LLMLoopTimeoutMinutes)*unit)
		} else {
			runCtx = sessionCtx
		}
		defer lr.al.registerSessionCancel(opts.SessionKey, cancel)()
		if timeoutCancel != nil {
			defer timeoutCancel()
		}
	}

	lr.al.toolCoordinator.updateToolContexts(agent, opts.Channel, opts.ChatID, opts.SessionKey)

	if agent.ContextBuilder != nil {
		sessionModel := lr.modelForTurn(agent, opts)
		if sessionModel != "" {
			providerName := extractProviderFromModel(sessionModel, lr.al.cfg().Agents.Defaults.Provider)
			agent.ContextBuilder.SetVisionSupported(getSupportsImages(lr.al.cfg(), sessionModel, providerName))
			agent.ContextBuilder.SetVideoSupported(getSupportsVideo(lr.al.cfg(), sessionModel, providerName))
		}
	}
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = agent.Sessions.GetHistory(opts.SessionKey)
		summary = agent.Sessions.GetSummary(opts.SessionKey)
		if healed, changed := healAssistantToolCalls(history); changed {
			logger.WarnCF("agent", "Healed malformed assistant tool calls in session history",
				map[string]interface{}{
					"agent_id":     agent.ID,
					"session_key":  opts.SessionKey,
					"before_count": len(history),
					"after_count":  len(healed),
				})
			history = healed
			agent.Sessions.SetHistory(opts.SessionKey, history)
		}
		if cleaned, removed := dropBlankAssistantMessages(history); removed {
			logger.WarnCF("agent", "Dropped blank assistant messages from session history",
				map[string]interface{}{
					"agent_id":     agent.ID,
					"session_key":  opts.SessionKey,
					"before_count": len(history),
					"after_count":  len(cleaned),
				})
			history = cleaned
			agent.Sessions.SetHistory(opts.SessionKey, history)
		}
		history = ensureSummaryMaterialized(agent, opts.SessionKey, history, summary)
		lr.al.verboseManager.InitializeFromSession(opts.SessionKey)
	}
	persistedAttachments, err := utils.PersistAttachmentsToWorkspace(agent.Workspace, opts.Attachments)
	if err != nil {
		logger.WarnCF("agent", "Failed to persist attachments to workspace", map[string]interface{}{"error": err.Error()})
		persistedAttachments = opts.Attachments
	}
	sessionMode := agent.Sessions.GetMode(opts.SessionKey)
	renderedUserMessage := agent.ContextBuilder.BuildCurrentUserMessage(opts.UserMessage, persistedAttachments, opts.Channel, opts.ChatID)
	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		persistedAttachments,
		opts.Channel,
		opts.ChatID,
		opts.SessionKey,
		sessionMode,
	)

	if !opts.SkipUserMessage {
		if opts.AppliedCommand != nil {
			agent.Sessions.AddFullMessage(opts.SessionKey, providers.Message{
				Role:           "user",
				Content:        renderedUserMessage,
				DisplayContent: opts.AppliedCommand.Original,
				Command:        commandAppliedOf(opts.AppliedCommand),
			})
		} else {
			agent.Sessions.AddMessage(opts.SessionKey, "user", renderedUserMessage)
		}
	} else if opts.Channel == "system" {
		agent.Sessions.AddMessage(opts.SessionKey, "system", renderedUserMessage)
	}
	if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save user message to disk", map[string]interface{}{
			"session_key": opts.SessionKey,
			"error":       saveErr.Error(),
		})
	}

	opts.midTurnQueue = noticeQueue
	finalContent, iteration, err := lr.runLLMIteration(runCtx, agent, messages, opts)
	if err != nil {
		if agent.LLMLoopTimeoutMinutes > 0 && errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("LLM loop exceeded %d minute timeout", agent.LLMLoopTimeoutMinutes)
		}
		if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
			logger.WarnCF("agent", "Failed to save session after LLM error", map[string]interface{}{
				"session_key": opts.SessionKey,
				"error":       saveErr.Error(),
			})
		}
		return "", err
	}

	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	if iteration == 0 && finalContent != "" {
		agent.Sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	}
	if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save session to disk", map[string]interface{}{
			"session_key": opts.SessionKey,
			"error":       saveErr.Error(),
		})
	}

	if opts.EnableSummary {
		lr.al.sessionManager.maybeSummarize(agent, opts.SessionKey, opts.Channel, opts.ChatID)
	}

	if opts.SendResponse {
		finalMsgID := iterationMsgID(opts.MessageID, iteration)
		outboundMsg := bus.OutboundMessage{
			Channel:   opts.Channel,
			ChatID:    opts.ChatID,
			Content:   finalContent,
			MessageID: finalMsgID,
		}
		if opts.ReplyTo != "" {
			outboundMsg.ReplyTo = opts.ReplyTo
		}
		lr.al.bus.PublishOutbound(outboundMsg)
	}

	if opts.SendResponse {
		return "", nil
	}

	return finalContent, nil
}

func lastAssistantResponse(agent *AgentInstance, sessionKey string) string {
	if agent == nil || agent.Sessions == nil {
		return ""
	}
	history := agent.Sessions.GetHistoryView(sessionKey)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		if strings.TrimSpace(history[i].Content) == "" {
			continue
		}
		return history[i].Content
	}
	return ""
}

func (lr *llmRunnerImpl) maybeRunGoalContinuation(agent *AgentInstance, opts processOptions, lastResponse string) {
	if opts.SkipGoalLoop {
		return
	}
	if lr.al.goalManager == nil || !lr.al.goalManager.IsActive(opts.SessionKey) {
		return
	}
	ctx := lr.al.goalStopCtx
	if ctx == nil {
		ctx = context.Background()
	}
	lr.runGoalContinuation(ctx, agent, opts, lastResponse)
}

func (lr *llmRunnerImpl) runGoalContinuation(ctx context.Context, agent *AgentInstance, opts processOptions, lastResponse string) {
	gm := lr.al.goalManager

	lr.al.markGoalLoopActive(opts.SessionKey)
	defer lr.al.clearGoalLoopActive(opts.SessionKey)

	for {
		if ctx.Err() != nil {
			return
		}

		goal := gm.Get(opts.SessionKey)
		if goal == nil || goal.Status != GoalActive {
			return
		}

		if exhausted := gm.IncrementTurn(opts.SessionKey); exhausted {
			updatedGoal := gm.Get(opts.SessionKey)
			notice := fmt.Sprintf("🚫 Goal budget exhausted (%d/%d turns).\nGoal: %s\n\nThe goal has been marked as blocked. Use /goal clear to remove it, or /goal <text> --turns N to retry with a larger budget.",
				updatedGoal.TurnsUsed, updatedGoal.MaxTurns, updatedGoal.Text)
			lr.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: opts.Channel,
				ChatID:  opts.ChatID,
				Content: notice,
			})
			return
		}

		judgeAnswer := ""
		if gm.judge != nil {
			lr.al.publishToolLifecycle(bus.OutboundMessage{
				Channel: opts.Channel,
				ChatID:  opts.ChatID,
				Event:   "tool.executing",
				Metadata: map[string]string{
					"tool":   "goal",
					"action": "🔍 Reviewing goal result...",
				},
			})

			var isDone bool
			var err error
			isDone, judgeAnswer, err = gm.judge.JudgeGoal(ctx, opts.SessionKey, goal.Text, lastResponse)
			lr.al.publishToolLifecycle(bus.OutboundMessage{
				Channel: opts.Channel,
				ChatID:  opts.ChatID,
				Event:   "tool.result",
				Metadata: map[string]string{
					"tool": "goal",
				},
			})

			if err != nil {
				logger.WarnCF("agent", "Goal judge failed, continuing loop", map[string]interface{}{
					"session_key": opts.SessionKey,
					"error":       err.Error(),
				})
			} else if isDone {
				gm.MarkDone(opts.SessionKey)
				notice := fmt.Sprintf("✅ Goal achieved!\n🎯 %s\n   Completed in %d turns.", goal.Text, goal.TurnsUsed)
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: notice,
				})
				return
			}
		}

		if !gm.IsActive(opts.SessionKey) {
			return
		}

		updatedGoal := gm.Get(opts.SessionKey)
		guidance := extractContinuationGuidance(judgeAnswer)
		var continuationPrompt string
		if guidance != "" {
			continuationPrompt = fmt.Sprintf(
				"[GOAL CONTINUATION — Turn %d/%d]\n"+
					"You have an active persistent goal:\n\n🎯 %s\n\n"+
					"The goal supervisor has directed the next step:\n▶ %s\n\n"+
					"Execute this specific step now. Do NOT ask for confirmation or wait for user input. "+
					"If after completing this step you believe the goal is fully achieved, state that clearly.",
				updatedGoal.TurnsUsed, updatedGoal.MaxTurns, updatedGoal.Text, guidance,
			)
		} else {
			continuationPrompt = fmt.Sprintf(
				"[GOAL CONTINUATION — Turn %d/%d]\n"+
					"You have an active persistent goal:\n\n🎯 %s\n\n"+
					"Continue working toward this goal. Do NOT ask for confirmation or wait for user input. "+
					"Take the next concrete step. If you believe the goal is fully achieved, state that clearly.",
				updatedGoal.TurnsUsed, updatedGoal.MaxTurns, updatedGoal.Text,
			)
		}

		logger.InfoCF("agent", "Goal continuation turn", map[string]interface{}{
			"session_key": opts.SessionKey,
			"turn":        updatedGoal.TurnsUsed,
			"max_turns":   updatedGoal.MaxTurns,
			"goal":        updatedGoal.Text,
		})

		contOpts := processOptions{
			SessionKey:      opts.SessionKey,
			Channel:         opts.Channel,
			ChatID:          opts.ChatID,
			UserMessage:     continuationPrompt,
			DefaultResponse: "Continuing to work on the goal...",
			EnableSummary:   true,
			SendResponse:    true,
			NoHistory:       false,
			SkipGoalLoop:    true,
		}

		_, err := lr.runAgentLoop(ctx, agent, contOpts)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.WarnCF("agent", "Goal continuation turn failed", map[string]interface{}{
				"session_key": opts.SessionKey,
				"error":       err.Error(),
			})
			notice := fmt.Sprintf("⚠️ Goal continuation encountered an error: %s\nUse /goal resume to retry.", err.Error())
			lr.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: opts.Channel,
				ChatID:  opts.ChatID,
				Content: notice,
			})
			gm.Pause(opts.SessionKey)
			return
		}

		lastResponse = lastAssistantResponse(agent, opts.SessionKey)
	}
}

func (lr *llmRunnerImpl) modelForTurn(agent *AgentInstance, opts processOptions) string {
	if opts.ModelOverride != "" {
		return opts.ModelOverride
	}
	if lr.al == nil || lr.al.sessionManager == nil {
		return ""
	}
	return lr.al.sessionManager.ModelForSession(agent, opts.SessionKey)
}

func (lr *llmRunnerImpl) runLLMIteration(ctx context.Context, agent *AgentInstance, messages []providers.Message, opts processOptions) (string, int, error) {
	toolRegistry := lr.al.toolCoordinator.sessionTools(agent, opts.SessionKey)
	if toolRegistry == nil {
		toolRegistry = agent.Tools
	}

	iteration := 0
	var finalContent string
	emptyRetries := 0
	loopDetector := newLoopDetector()
	model := lr.modelForTurn(agent, opts)
	model = lr.al.cfg().Providers.ResolveModelAlias(model, lr.al.cfg().Agents.Defaults.Provider)
	candidates := agent.Candidates
	if model != agent.Model {
		if ref := providers.ParseModelRef(model, lr.al.cfg().Agents.Defaults.Provider); ref != nil {
			candidates = make([]providers.FallbackCandidate, 0, len(agent.Candidates)+1)
			candidates = append(candidates, providers.FallbackCandidate{
				Provider: ref.Provider,
				Model:    ref.Model,
			})
			for _, candidate := range agent.Candidates {
				if candidate.Provider == ref.Provider && candidate.Model == ref.Model {
					continue
				}
				candidates = append(candidates, candidate)
			}
		}
	}

	for agent.MaxIterations <= 0 || iteration < agent.MaxIterations {
		if err := ctx.Err(); err != nil {
			return "", iteration, err
		}

		iteration++

		if iteration > 1 {
			agent.Sessions.DiscardStreamingDraft(opts.SessionKey)
		}

		if iteration > 1 && midTurnNoticeBoundaryIsSafe(messages) {
			if notices := opts.midTurnQueue.drain(); len(notices) > 0 {
				messages = lr.injectMidTurnNotices(agent, messages, opts, notices)
			}
		}

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"agent_id":  agent.ID,
				"iteration": iteration,
				"max":       agent.MaxIterations,
			})

		providerToolDefs := toolRegistry.ToProviderDefs()

		modelProvider := extractProviderFromModel(model, lr.al.cfg().Agents.Defaults.Provider)
		modelHasVision := getSupportsImages(lr.al.cfg(), model, modelProvider)
		modelHasVideo := getSupportsVideo(lr.al.cfg(), model, modelProvider)
		providerToolDefs = filterToolDefs(providerToolDefs, modelHasVision, modelHasVideo, nil)

		if !modelHasVision || !modelHasVideo {
			messages = providers.StripContentParts(messages, !modelHasVision, !modelHasVideo)
		}

		if agent.Sessions.GetMode(opts.SessionKey) == "chat" {
			chatTools := map[string]bool{"web_search": true, "web_fetch": true}
			filtered := make([]providers.ToolDefinition, 0, 2)
			for _, def := range providerToolDefs {
				if chatTools[def.Function.Name] {
					filtered = append(filtered, def)
				}
			}
			providerToolDefs = filtered
		}

		messages = lr.maybeCompactLoopContext(ctx, agent, messages, model, opts, iteration)

		logger.DebugCF("agent", "LLM request",
			map[string]interface{}{
				"agent_id":          agent.ID,
				"iteration":         iteration,
				"model":             model,
				"messages_count":    len(messages),
				"tools_count":       len(providerToolDefs),
				"max_tokens":        agent.MaxTokens,
				"temperature":       agent.Temperature,
				"system_prompt_len": len(messages[0].Content),
			})

		logger.DebugCF("agent", "Full LLM request",
			map[string]interface{}{
				"iteration":     iteration,
				"messages_json": FormatMessagesForLog(messages),
				"tools_json":    FormatToolsForLog(providerToolDefs),
			})

		var streamOnChunk func(chunk string, done bool)
		var streamOnReasoning func(reasoningChunk string)
		iterationMsgID := iterationMsgID(opts.MessageID, iteration)
		streamer := newStreamHandler(lr.al.bus, opts.Channel, opts.SessionKey, iterationMsgID)
		if streamer.shouldStream(opts.SendResponse) {
			streamOnChunk = streamer.onChunk
			streamOnReasoning = streamer.onReasoning
		}

		llmCallerInstance := newLLMCaller(lr.al)
		if lr.retryWait != nil {
			llmCallerInstance.retryWait = lr.retryWait
		}
		callOpts := llmCallOptions{
			ctx:            ctx,
			agent:          agent,
			messages:       messages,
			toolDefs:       providerToolDefs,
			model:          model,
			candidates:     candidates,
			sessionKey:     opts.SessionKey,
			channel:        opts.Channel,
			chatID:         opts.ChatID,
			iteration:      iteration,
			streamOnChunk:  streamOnChunk,
			streamOnReason: streamOnReasoning,
		}

		var response *providers.LLMResponse
		var err error

		for transientAttempt := 0; ; transientAttempt++ {
			lr.al.writeTurnMarker(opts.SessionKey, turnPhaseLLMWait, iteration, nil)
			response, messages, err = llmCallerInstance.executeWithRetry(callOpts, messages)
			if err == nil {
				break
			}
			if ctx.Err() != nil || !providers.IsRetriableError(err) {
				break
			}
			if transientAttempt >= transientLLMRetries {
				break
			}
			backoff := transientLLMBackoff(transientAttempt)
			logger.WarnCF("agent", "Transient LLM error; retrying within execution",
				map[string]interface{}{
					"agent_id":  agent.ID,
					"iteration": iteration,
					"attempt":   transientAttempt + 1,
					"max":       transientLLMRetries,
					"waiting":   backoff.String(),
					"error":     err.Error(),
				})
			if !lr.waitForBackoff(ctx, backoff) {
				return "", iteration, ctx.Err()
			}
		}

		if err != nil {
			logger.ErrorCF("agent", "LLM call failed",
				map[string]interface{}{
					"agent_id":  agent.ID,
					"iteration": iteration,
					"error":     err.Error(),
				})
			return "", iteration, fmt.Errorf("LLM call failed after retries: %w", err)
		}

		trackTokenUsage(agent.Sessions, opts.SessionKey, agent.ID, messages, response)

		if len(response.ToolCalls) == 0 {
			finalContent = response.Content

			if len(strings.TrimSpace(finalContent)) == 0 {
				emptyRetries++
				if emptyRetries > maxConsecutiveEmptyResponses {
					logger.ErrorCF("agent", "Empty response limit reached, ending turn",
						map[string]interface{}{
							"agent_id":  agent.ID,
							"iteration": iteration,
							"retries":   emptyRetries,
							"max":       maxConsecutiveEmptyResponses,
						})
					if streamer.shouldStream(opts.SendResponse) {
						lr.discardIterationStream(opts, streamer.messageID, discardReasonEmptyResponse)
					}
					return "", iteration, nil
				}
				logger.WarnCF("agent", "Empty response received, retrying with follow-up prompt",
					map[string]interface{}{
						"agent_id":  agent.ID,
						"iteration": iteration,
						"retry":     emptyRetries,
					})
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
				})
				if !lr.waitForBackoff(ctx, emptyRetryBackoff(emptyRetries-1)) {
					return "", iteration, ctx.Err()
				}
				messages = append(messages, providers.Message{
					Role:    "user",
					Content: "Your previous response was empty. Please provide a helpful response to my request.",
				})
				if streamer.shouldStream(opts.SendResponse) {
					lr.discardIterationStream(opts, streamer.messageID, discardReasonEmptyResponse)
				}
				continue
			}
			emptyRetries = 0

			assistantMsg := providers.Message{
				Role:             "assistant",
				Content:          response.Content,
				ReasoningContent: response.ReasoningContent,
			}
			messages = append(messages, assistantMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

			if containsPlainToolCall(finalContent, toolRegistry) {
				logger.WarnCF("agent", "LLM response contains plain-text tool calls instead of function calls, injecting guidance",
					map[string]interface{}{
						"agent_id":  agent.ID,
						"iteration": iteration,
					})
				messages = messages[:len(messages)-1]
				agent.Sessions.RemoveLastMessage(opts.SessionKey)
				toolNames := knownToolNames(toolRegistry)
				guidanceMsg := fmt.Sprintf(
					"⚠️ You wrote tool invocations as plain text (e.g., `read_file{...}`) instead of using the proper function calling mechanism.\n\n"+
						"You MUST use the native tool/function calls available to invoke tools. Do NOT write tool names with JSON arguments as plain text.\n\n"+
						"Available tools: %s\n\n"+
						"Please retry your response using proper function calls.",
					strings.Join(toolNames, ", "))
				messages = append(messages, providers.Message{
					Role:    "user",
					Content: guidanceMsg,
				})
				agent.Sessions.AddMessage(opts.SessionKey, "user", guidanceMsg)
				if streamer.shouldStream(opts.SendResponse) {
					lr.discardIterationStream(opts, streamer.messageID, discardReasonPlainTextToolCall)
				}
				continue
			}

			hasBudget := agent.MaxIterations <= 0 || iteration < agent.MaxIterations
			if hasBudget {
				if notices := opts.midTurnQueue.drainOrDeactivate(); len(notices) > 0 {
					messages = lr.injectMidTurnNotices(agent, messages, opts, notices)
					continue
				}
			}

			break
		}

		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}

		if guidanceMsg := loopDetector.Check(response.ToolCalls, agent.ID, iteration); guidanceMsg != nil {
			messages = append(messages, *guidanceMsg)
			agent.Sessions.AddMessage(opts.SessionKey, "user", guidanceMsg.Content)
		}

		assistantMsg := providers.Message{
			Role:             "assistant",
			Content:          response.Content,
			ReasoningContent: response.ReasoningContent,
		}
		assistantMsg.ToolCalls = providers.CanonicalToolCalls(response.ToolCalls)

		if len(assistantMsg.ToolCalls) == 0 {
			logger.WarnCF("agent", "all tool calls from response were invalid, requesting a retry",
				map[string]interface{}{
					"iteration":   iteration,
					"rejected":    len(response.ToolCalls),
					"agent_id":    agent.ID,
					"session_key": opts.SessionKey,
				})
			if strings.TrimSpace(response.Content) != "" {
				messages = append(messages, assistantMsg)
				agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)
			} else {
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
				})
				if streamer.shouldStream(opts.SendResponse) {
					lr.discardIterationStream(opts, streamer.messageID, discardReasonInvalidToolCalls)
				}
			}
			guidanceMsg := providers.Message{
				Role: "user",
				Content: "⚠️ Your previous tool call was malformed and could not be parsed " +
					"(a tool name and a valid JSON object of arguments are required). " +
					"Please retry using a proper function call.",
			}
			messages = append(messages, guidanceMsg)
			agent.Sessions.AddMessage(opts.SessionKey, "user", guidanceMsg.Content)
			continue
		}

		messages = append(messages, assistantMsg)

		agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		executor := newToolExecutor(lr.al)

		lr.al.writeTurnMarker(opts.SessionKey, turnPhaseToolsRun, iteration, nil)
		lr.al.writeTurnSubagentSnapshot(opts.SessionKey)

		type toolExecResult struct {
			tc  providers.ToolCall
			res *tools.ToolResult
			err error
		}
		var execResults []toolExecResult
		var execErr error
		for _, tc := range assistantMsg.ToolCalls {
			if execErr != nil || ctx.Err() != nil {
				execResults = append(execResults, toolExecResult{
					tc:  tc,
					res: &tools.ToolResult{ForLLM: "[Tool execution was cancelled]"},
				})
				continue
			}
			toolResult, err := executor.Execute(toolExecOptions{
				ctx:          ctx,
				agent:        agent,
				registry:     toolRegistry,
				tc:           tc,
				channel:      opts.Channel,
				chatID:       opts.ChatID,
				sessionKey:   opts.SessionKey,
				iteration:    iteration,
				sendResponse: opts.SendResponse,
			})
			if err != nil {
				execErr = err
				execResults = append(execResults, toolExecResult{
					tc:  tc,
					res: &tools.ToolResult{ForLLM: "[Tool execution failed]"},
				})
				continue
			}
			execResults = append(execResults, toolExecResult{tc: tc, res: toolResult})
		}

		var allContextMsgs []providers.Message
		for _, er := range execResults {
			contentForLLM := lr.al.redactor.Redact(buildToolResultContent(er.res))
			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: er.tc.ID,
			}
			messages = append(messages, toolResultMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, toolResultMsg)

			if len(er.res.ContextMessages) > 0 {
				allContextMsgs = append(allContextMsgs, er.res.ContextMessages...)
			}
		}

		if !modelHasVision || !modelHasVideo {
			allContextMsgs = providers.StripContentParts(allContextMsgs, !modelHasVision, !modelHasVideo)
		}
		for _, ctxMsg := range allContextMsgs {
			messages = append(messages, ctxMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, ctxMsg)
		}

		if execErr != nil {
			return "", iteration, execErr
		}

		messages = lr.maybeCompactLoopContext(ctx, agent, messages, model, opts, iteration)

		if err := ctx.Err(); err != nil {
			return "", iteration, err
		}
	}

	return finalContent, iteration, nil
}

func midTurnNoticeBoundaryIsSafe(messages []providers.Message) bool {
	if len(messages) == 0 {
		return true
	}
	return messages[len(messages)-1].Role != "user"
}

func (lr *llmRunnerImpl) injectMidTurnNotices(agent *AgentInstance, messages []providers.Message, opts processOptions, notices []midTurnNotice) []providers.Message {
	if len(notices) == 0 {
		return messages
	}

	raw := make([]string, 0, len(notices))
	for _, n := range notices {
		raw = append(raw, n.content)
	}
	content := midTurnNoticePrefix + strings.Join(raw, midTurnNoticeSeparator)

	messages = append(messages, providers.Message{Role: "user", Content: content})
	agent.Sessions.AddMessage(opts.SessionKey, "user", content)

	if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save mid-turn subagent notices", map[string]interface{}{
			"session_key": opts.SessionKey,
			"notices":     len(notices),
			"error":       saveErr.Error(),
		})
	}

	lr.al.bus.PublishOutbound(bus.OutboundMessage{
		Channel:  opts.Channel,
		ChatID:   opts.SessionKey,
		Event:    "history.updated",
		Metadata: map[string]string{"reason": "subagent_notice"},
	})

	return messages
}

func iterationMsgID(baseID string, iteration int) string {
	if iteration > 1 && baseID != "" {
		return fmt.Sprintf("%s-%d", baseID, iteration)
	}
	return baseID
}

func (lr *llmRunnerImpl) maybeCompactLoopContext(ctx context.Context, agent *AgentInstance, messages []providers.Message, model string, opts processOptions, iteration int) []providers.Message {
	if reduced, evicted := tools.EnforceImageBudget(messages); evicted > 0 {
		messages = reduced
		logger.InfoCF("agent", "Request image budget enforced", map[string]interface{}{
			"agent_id":      agent.ID,
			"session_key":   opts.SessionKey,
			"iteration":     iteration,
			"evicted_parts": evicted,
			"request_bytes": providers.EstimateRequestBytes(messages),
			"budget_bytes":  config.DefaultMaxRequestBytes,
		})
	}

	contextWindow := lr.al.getSessionContextWindow(opts.SessionKey)
	if contextWindow > 0 || tools.RequestOverBudget(messages) {
		tokens := tools.EstimateLoopTokens(messages)
		thresholdPercent := lr.al.cfg().SessionCompactionThresholdPercent()
		threshold := contextWindow * thresholdPercent / 100
		if tokens > threshold || tools.RequestOverBudget(messages) {
			logger.InfoCF("agent", "Intra-loop context compaction triggered", map[string]interface{}{
				"agent_id":       agent.ID,
				"session_key":    opts.SessionKey,
				"iteration":      iteration,
				"tokens":         tokens,
				"threshold":      threshold,
				"context_window": contextWindow,
			})
			if opts.Channel == "native" {
				lr.al.publishToolLifecycle(bus.OutboundMessage{
					Channel:  opts.Channel,
					ChatID:   opts.ChatID,
					Event:    "tool.executing",
					Metadata: map[string]string{"tool": "compact", "action": "Compacting context..."},
				})
			}
			compactProvider := agent.Provider
			compactModel := model
			if cm := lr.al.cfg().CompactionModel(); cm != "" {
				compactModel = cm
			}
			if ref := providers.ParseModelRef(compactModel, ""); ref != nil && ref.Provider != "" {
				agentRef := providers.ParseModelRef(agent.Model, "")
				if agentRef == nil || agentRef.Provider != ref.Provider {
					if newProv, err := providers.CreateProviderForCandidate(lr.al.cfg(), ref.Provider); err == nil {
						compactProvider = newProv
					}
				}
			}
			if compacted, ok := tools.CompactLoopMessages(ctx, compactProvider, compactModel, messages, 6, opts.SessionKey); ok {
				messages = compacted
				if opts.Channel == "native" {
					lr.al.publishToolLifecycle(bus.OutboundMessage{
						Channel: opts.Channel,
						ChatID:  opts.ChatID,
						Event:   "tool.result",
						Metadata: map[string]string{
							"tool":   "compact",
							"result": fmt.Sprintf("Tokens: ~%d → ~%d", tokens, tools.EstimateLoopTokens(messages)),
						},
					})
				}
				if len(compacted) > 1 && strings.HasPrefix(compacted[1].Content, "[Context compacted") {
					agent.Sessions.SetSummary(opts.SessionKey, compacted[1].Content)
					agent.Sessions.ExcludeOldMessagesFromContext(opts.SessionKey, 6)
					agent.Sessions.IncrementCompactionCount(opts.SessionKey)
					if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
						logger.WarnCF("agent", "Failed to save session after intra-loop compaction", map[string]interface{}{
							"session_key": opts.SessionKey,
							"error":       saveErr.Error(),
						})
					} else if lr.al.cfg().EvictExcludedFromMemory() {
						agent.Sessions.EvictExcludedMessages(opts.SessionKey)
					}
					logger.InfoCF("agent", "Intra-loop compaction synced to session", map[string]interface{}{
						"session_key":   opts.SessionKey,
						"summary_chars": len(compacted[1].Content),
						"kept_messages": 6,
					})
				}
			}
		}
	}

	return messages
}

const (
	discardReasonEmptyResponse     = "empty_response"
	discardReasonPlainTextToolCall = "plain_text_tool_call"
	discardReasonInvalidToolCalls  = "invalid_tool_calls"
)

func (lr *llmRunnerImpl) discardIterationStream(opts processOptions, msgID, reason string) {
	if lr == nil || lr.al == nil || lr.al.bus == nil {
		return
	}
	lr.al.bus.PublishOutbound(bus.OutboundMessage{
		Channel:   opts.Channel,
		ChatID:    opts.ChatID,
		Event:     "message.discard",
		MessageID: msgID,
		Metadata:  map[string]string{"reason": reason},
	})
}
