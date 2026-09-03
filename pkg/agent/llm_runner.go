// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

// llmRunner is an internal interface for LLM execution
type llmRunner interface {
	runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error)
	maybeRunGoalContinuation(agent *AgentInstance, opts processOptions, lastResponse string)
}

// transientLLMRetries is the number of times a transient LLM error is retried
// within the same execution before giving up and terminating the run.
const transientLLMRetries = 3

// transientLLMBackoff returns the wait duration before the given transient
// retry attempt (5s, 15s, 30s, capped at 30s).
func transientLLMBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
}

// maxConsecutiveEmptyResponses bounds how many consecutive empty (blank)
// assistant responses the agent loop will retry before ending the turn with
// the default response. A blank HTTP-200 is usually transient (thinking model
// exhausting its token budget on reasoning, provider hiccup), so a few
// retries recover it — but retrying forever (the old behavior) hangs the
// session: the per-session semaphore is held, follow-up user messages queue
// up, and the conversation appears dead ("session won't continue").
const maxConsecutiveEmptyResponses = 5

// emptyRetryBackoff returns the wait duration before the given empty-response
// retry attempt (1s, 2s, 3s, capped at 3s). Empty responses are retried up to
// maxConsecutiveEmptyResponses times, bounded by context cancellation (user
// abort / session cancel); after the limit the turn ends cleanly.
func emptyRetryBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
}

// llmRunnerImpl implements the llmRunner interface
type llmRunnerImpl struct {
	al *AgentLoop

	// retryWait is called to wait between retry attempts.
	// nil means use default (time.After, set on llmCaller).
	// Override in tests to avoid real sleeps.
	retryWait func(time.Duration) <-chan time.Time

	// loopTimeoutUnit converts LLMLoopTimeoutMinutes into a duration.
	// Production uses time.Minute; tests override it so the loop-timeout path
	// can be exercised without waiting a real minute.
	loopTimeoutUnit time.Duration
}

// newLLMRunner creates a new LLM runner
func newLLMRunner(al *AgentLoop) *llmRunnerImpl {
	return &llmRunnerImpl{al: al, loopTimeoutUnit: time.Minute}
}

// waitForBackoff blocks for d or until ctx is done, honouring the test-injectable
// retryWait hook. Production uses time.After; tests set retryWait to
// instantRetryWait so retry logic can be exercised without real sleeps.
// Returns true if the wait completed, false if ctx was cancelled.
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

// runAgentLoop is the core message processing logic.
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

	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		// Don't record internal channels (cli, system, subagent)
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
		// Apply the configurable LLM loop timeout. The timeout context is a
		// child of sessionCtx so that cancelling the session (user stop /
		// shutdown) also cancels the loop. When the timeout expires, the loop
		// returns a clear error instead of hanging indefinitely.
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

	// 1. Update tool contexts
	lr.al.toolCoordinator.updateToolContexts(agent, opts.Channel, opts.ChatID, opts.SessionKey)

	// 2. Build messages (skip history for heartbeat)
	// Sync the system prompt's vision flag with the session's current model.
	// The flag is initialized at instance creation, but the session model can
	// change at runtime (/model, TUI model picker, REST). Without this sync,
	// the tools section of the system prompt keeps hiding read_image based on
	// a stale flag even after switching to a vision-capable model.
	if agent.ContextBuilder != nil {
		if sessionModel := lr.al.sessionManager.ModelForSession(agent, opts.SessionKey); sessionModel != "" {
			providerName := extractProviderFromModel(sessionModel, lr.al.cfg().Agents.Defaults.Provider)
			agent.ContextBuilder.SetVisionSupported(getSupportsImages(lr.al.cfg(), sessionModel, providerName))
		}
	}
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = agent.Sessions.GetHistory(opts.SessionKey)
		summary = agent.Sessions.GetSummary(opts.SessionKey)
		// Heal sessions contaminated by the legacy blank-persistence bug:
		// assistant messages with no content and no tool calls are dropped
		// from the history (and rewritten to the store) so the model cannot
		// imitate them into a fresh empty-response loop.
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
		// Initialize verbose mode from persistent storage
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

	// 3. Save user message to session and persist immediately
	if !opts.SkipUserMessage {
		agent.Sessions.AddMessage(opts.SessionKey, "user", renderedUserMessage)
	} else if opts.Channel == "system" {
		agent.Sessions.AddMessage(opts.SessionKey, "system", renderedUserMessage)
	}
	if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save user message to disk", map[string]interface{}{
			"session_key": opts.SessionKey,
			"error":       saveErr.Error(),
		})
	}

	// 4. Run LLM iteration loop
	finalContent, iteration, err := lr.runLLMIteration(runCtx, agent, messages, opts)
	if lr.al.toolCoordinator != nil {
		lr.al.toolCoordinator.markSessionSubagentsDelivered(opts.SessionKey)
	}
	if err != nil {
		// Surface a clear error when the LLM loop timed out.
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

	// If last tool had ForUser content and we already sent it, we might not need to send final response
	// This is controlled by the tool's Silent flag and ForUser content

	// 5. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 6. Save final assistant message to session (only if not already saved in the loop)
	// The loop saves assistant messages with ReasoningContent when:
	// - There are tool calls (line ~565)
	// - There are no tool calls (line ~477, newly added)
	// Only save here if we never entered the loop (iteration == 0)
	if iteration == 0 && finalContent != "" {
		agent.Sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	}
	if saveErr := agent.Sessions.Save(opts.SessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save session to disk", map[string]interface{}{
			"session_key": opts.SessionKey,
			"error":       saveErr.Error(),
		})
	}

	// 7. Optional: summarization
	if opts.EnableSummary {
		lr.al.sessionManager.maybeSummarize(agent, opts.SessionKey, opts.Channel, opts.ChatID)
	}

	// 8. Optional: send response via bus
	if opts.SendResponse {
		// Use the iteration-suffixed messageID so the frontend's message.complete
		// handler can match the streaming bubble created during runLLMIteration.
		// Without this, multi-iteration LLM runs (iteration > 1) produce duplicate
		// message bubbles: one from streaming (msgID-2) and one from message.complete
		// (msgID), because the IDs don't match.
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

	// 9. Goal continuation is triggered by the caller (message processor)
	// AFTER this function returns and releases the per-session semaphore.
	// See maybeRunGoalContinuation. This avoids the recursive runAgentLoop
	// deadlock on the per-session semaphore.

	if opts.SendResponse {
		// Return empty string to prevent duplicate publish in loop.go
		return "", nil
	}

	return finalContent, nil
}

// lastAssistantResponse returns the content of the most recent assistant
// message in the session's history, or an empty string if there is none.
// It is used by the caller-side goal trigger to pass the latest agent output
// to the continuation loop / judge.
func lastAssistantResponse(agent *AgentInstance, sessionKey string) string {
	if agent == nil || agent.Sessions == nil {
		return ""
	}
	history := agent.Sessions.GetHistoryView(sessionKey)
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		// Skip blank assistant turns: a legacy bug persisted empty responses,
		// and the goal judge must not receive an empty "last response".
		if strings.TrimSpace(history[i].Content) == "" {
			continue
		}
		return history[i].Content
	}
	return ""
}

// maybeRunGoalContinuation is the caller-side trigger for the autonomous goal
// loop. It is invoked by the message processor AFTER runAgentLoop returns
// (and releases the per-session semaphore), so the recursive runAgentLoop
// calls inside the continuation each acquire and release the semaphore
// independently. Without this, the recursive call would re-acquire the still
// held semaphore and deadlock.
func (lr *llmRunnerImpl) maybeRunGoalContinuation(agent *AgentInstance, opts processOptions, lastResponse string) {
	if opts.SkipGoalLoop {
		return
	}
	if lr.al.goalManager == nil || !lr.al.goalManager.IsActive(opts.SessionKey) {
		return
	}
	// Use the goal stop context as parent so /goal clear and loop shutdown can
	// cancel an in-flight continuation loop.
	ctx := lr.al.goalStopCtx
	if ctx == nil {
		ctx = context.Background()
	}
	lr.runGoalContinuation(ctx, agent, opts, lastResponse)
}

// runGoalContinuation implements the autonomous goal loop. After each agent
// turn, it evaluates whether the goal is achieved (via the judge). If not,
// it injects a continuation prompt and runs another turn, repeating until
// the goal is done, the budget is exhausted, or the context is cancelled.
func (lr *llmRunnerImpl) runGoalContinuation(ctx context.Context, agent *AgentInstance, opts processOptions, lastResponse string) {
	gm := lr.al.goalManager

	// Mark the session as inside a goal loop for the entire duration so
	// IsSessionProcessing stays true during the judge-evaluation gap between
	// turns (when the per-session semaphore is released).
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

		// Increment turn counter; if budget exhausted, notify and stop.
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

		// Evaluate goal completion via judge (if configured).
		judgeAnswer := ""
		if gm.judge != nil {
			// Notify TUI that the goal is being reviewed.
			lr.al.bus.PublishOutbound(bus.OutboundMessage{
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
			// Clear the reviewing indicator.
			lr.al.bus.PublishOutbound(bus.OutboundMessage{
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
				// On judge error, continue the loop (don't block progress)
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

		// Re-check goal is still active (user may have cleared it).
		if !gm.IsActive(opts.SessionKey) {
			return
		}

		// Inject continuation prompt and run another turn.
		updatedGoal := gm.Get(opts.SessionKey)
		// If the judge supplied specific next-step guidance (subagent judge
		// acting as a supervisor), use it as the next turn's prompt instead of
		// the generic continuation prompt.
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

		// Run another agent turn with the continuation prompt.
		contOpts := processOptions{
			SessionKey:      opts.SessionKey,
			Channel:         opts.Channel,
			ChatID:          opts.ChatID,
			UserMessage:     continuationPrompt,
			DefaultResponse: "Continuing to work on the goal...",
			EnableSummary:   true,
			SendResponse:    true,
			NoHistory:       false,
			SkipGoalLoop:    true, // prevent recursion
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
			// On error, stop the loop to avoid infinite retries
			notice := fmt.Sprintf("⚠️ Goal continuation encountered an error: %s\nUse /goal resume to retry.", err.Error())
			lr.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: opts.Channel,
				ChatID:  opts.ChatID,
				Content: notice,
			})
			gm.Pause(opts.SessionKey)
			return
		}

		// runAgentLoop returns "" when SendResponse=true (it publishes the
		// outbound itself). Re-fetch the actual last assistant response from
		// session history so the judge sees the real content.
		lastResponse = lastAssistantResponse(agent, opts.SessionKey)
	}
}

// runLLMIteration executes the LLM call loop with tool handling.
func (lr *llmRunnerImpl) runLLMIteration(ctx context.Context, agent *AgentInstance, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	emptyRetries := 0
	loopDetector := newLoopDetector()
	model := lr.al.sessionManager.ModelForSession(agent, opts.SessionKey)
	// Resolve model alias to ensure a provider prefix is present for routing.
	// Persisted session models may lack the prefix (e.g., stored before alias
	// resolution was fixed), which causes ParseModelRef to fall back to the
	// default provider and route requests to the wrong endpoint.
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

	// MaxIterations <= 0 means unlimited (timeout + loop detector are the real safety guards)
	for agent.MaxIterations <= 0 || iteration < agent.MaxIterations {
		if err := ctx.Err(); err != nil {
			return "", iteration, err
		}

		iteration++

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"agent_id":  agent.ID,
				"iteration": iteration,
				"max":       agent.MaxIterations,
			})

		// Build tool definitions
		providerToolDefs := agent.Tools.ToProviderDefs()

		// Determine whether the current (primary) model supports vision. The
		// read_image tool is exposed based on the primary model only. When the
		// fallback chain fails over to a non-vision model, image content is
		// stripped per-candidate in callWithFallback (see llm_caller.go), so a
		// vision-capable primary model no longer loses read_image just because
		// a fallback in the chain lacks vision.
		modelHasVision := getSupportsImages(lr.al.cfg(), model, extractProviderFromModel(model, lr.al.cfg().Agents.Defaults.Provider))
		if !modelHasVision {
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, def := range providerToolDefs {
				if def.Function.Name != "read_image" {
					filtered = append(filtered, def)
				}
			}
			providerToolDefs = filtered

			// Strip image_url ContentParts from messages. Historical messages
			// may contain image data from previous turns (or from a different
			// model that supported vision). Sending image content to a
			// non-vision model causes API errors.
			messages = stripImageContentParts(messages)
		}

		// In chat mode, only expose web_search and web_fetch tools.
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

		// Pre-LLM compaction check: guards the request itself. A single
		// iteration can add hundreds of thousands of tokens of tool results
		// and context messages; without this check the request goes out
		// oversized and fails before the post-tool check ever runs.
		messages = lr.maybeCompactLoopContext(ctx, agent, messages, model, opts, iteration)

		// Log LLM request details
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

		// Log full messages (detailed)
		logger.DebugCF("agent", "Full LLM request",
			map[string]interface{}{
				"iteration":     iteration,
				"messages_json": FormatMessagesForLog(messages),
				"tools_json":    FormatToolsForLog(providerToolDefs),
			})

		// Setup streaming handlers for native channel.
		// Each iteration gets a unique messageID so that separate
		// LLM responses (with their own reasoning + text) render as
		// separate sections in the chat, not merged into one slot.
		var streamOnChunk func(chunk string, done bool)
		var streamOnReasoning func(reasoningChunk string)
		iterationMsgID := iterationMsgID(opts.MessageID, iteration)
		streamer := newStreamHandler(lr.al.bus, opts.Channel, opts.SessionKey, iterationMsgID)
		if streamer.shouldStream(opts.SendResponse) {
			streamOnChunk = streamer.onChunk
			streamOnReasoning = streamer.onReasoning
		}

		// Call LLM using llmCaller with retry logic
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
			response, messages, err = llmCallerInstance.executeWithRetry(callOpts, messages)
			if err == nil {
				break
			}
			// User abort or non-retriable error: stop the execution immediately.
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

		// Track token usage from response
		trackTokenUsage(agent.Sessions, opts.SessionKey, agent.ID, messages, response)

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content

			// If the response is empty, do NOT persist it and retry a bounded
			// number of times. Persisting an assistant message with empty content
			// (which happens when a thinking model burns the whole token budget on
			// reasoning and returns content:"" with finish_reason:length) poisons
			// the session history: every later turn replays those blanks, the model
			// imitates them, and the session becomes permanently stuck in an empty
			// loop that never terminates when MaxIterations is unlimited (0).
			// Instead we keep only the follow-up prompt in memory for the retry and
			// bound consecutive empties so the turn always ends cleanly.
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
					// Return empty content so runAgentLoop falls back to
					// DefaultResponse and the session is left clean.
					return "", iteration, nil
				}
				logger.WarnCF("agent", "Empty response received, retrying with follow-up prompt",
					map[string]interface{}{
						"agent_id":  agent.ID,
						"iteration": iteration,
						"retry":     emptyRetries,
					})
				// Keep the blank turn in the in-memory context only (preserves
				// user/assistant role alternation and shows the model what it
				// just produced) but never persist it to the session.
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
				})
				// Small backoff between empty-response retries (capped).
				if !lr.waitForBackoff(ctx, emptyRetryBackoff(emptyRetries-1)) {
					return "", iteration, ctx.Err()
				}
				messages = append(messages, providers.Message{
					Role:    "user",
					Content: "Your previous response was empty. Please provide a helpful response to my request.",
				})
				continue
			}
			// Non-empty final content: reset the consecutive-empty counter so the
			// backoff restarts small after a real response.
			emptyRetries = 0

			// Save assistant message with reasoning content (important for thinking
			// models like DeepSeek). Only reached for non-empty content, so blanks
			// are never written to the session.
			assistantMsg := providers.Message{
				Role:             "assistant",
				Content:          response.Content,
				ReasoningContent: response.ReasoningContent,
			}
			messages = append(messages, assistantMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

			// Check if the response contains plain-text tool invocations (e.g., `read_file{"path":"..."}`)
			// instead of proper function calls. Some models (like DeepSeek) sometimes output tool call
			// syntax as plain text, which would break the loop and send raw tool invocation text to the user.
			if containsPlainToolCall(finalContent, agent) {
				logger.WarnCF("agent", "LLM response contains plain-text tool calls instead of function calls, injecting guidance",
					map[string]interface{}{
						"agent_id":  agent.ID,
						"iteration": iteration,
					})
				// Remove the assistant message we just added (replaced with guidance)
				messages = messages[:len(messages)-1]
				agent.Sessions.RemoveLastMessage(opts.SessionKey)
				// Get available tool names for guidance
				toolNames := knownToolNames(agent)
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
				continue
			}

			break
		}

		// Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}

		// Check for loop patterns and inject guidance if needed
		if guidanceMsg := loopDetector.Check(response.ToolCalls, agent.ID, iteration); guidanceMsg != nil {
			messages = append(messages, *guidanceMsg)
			agent.Sessions.AddMessage(opts.SessionKey, "user", guidanceMsg.Content)
		}

		// Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:             "assistant",
			Content:          response.Content,
			ReasoningContent: response.ReasoningContent,
		}
		for _, tc := range response.ToolCalls {
			argumentsJSON, _ := json.Marshal(tc.Arguments)
			assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, providers.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argumentsJSON),
				},
				Name:      tc.Name,
				Arguments: tc.Arguments,
			})
		}
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls
		executor := newToolExecutor(lr.al)

		// Phase 1: Execute all tools and collect results
		type toolExecResult struct {
			tc  providers.ToolCall
			res *tools.ToolResult
			err error
		}
		var execResults []toolExecResult
		var execErr error // tracks the first execution error encountered
		for _, tc := range response.ToolCalls {
			if execErr != nil || ctx.Err() != nil {
				// Already errored or cancelled — add placeholder for remaining tools
				execResults = append(execResults, toolExecResult{
					tc:  tc,
					res: &tools.ToolResult{ForLLM: "[Tool execution was cancelled]"},
				})
				continue
			}
			toolResult, err := executor.Execute(toolExecOptions{
				ctx:          ctx,
				agent:        agent,
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

		// Phase 2: Append all tool result messages (role: "tool") in order
		// This includes placeholder results for cancelled/failed tools to keep the session consistent.
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

			// Collect context messages for phase 3
			if len(er.res.ContextMessages) > 0 {
				allContextMsgs = append(allContextMsgs, er.res.ContextMessages...)
			}
		}

		// Phase 3: Append all context messages (role: "user") after all tool messages
		// This ensures tool messages are contiguous, satisfying the API requirement
		// that all tool responses follow immediately after the assistant's tool_calls.
		if !modelHasVision {
			allContextMsgs = stripImageContentParts(allContextMsgs)
		}
		for _, ctxMsg := range allContextMsgs {
			messages = append(messages, ctxMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, ctxMsg)
		}

		// Return error if one occurred, now that placeholder results have been saved
		if execErr != nil {
			return "", iteration, execErr
		}

		// Post-tool compaction check: runs after tool results and context
		// messages are appended so oversized contexts are compacted before the
		// next iteration's pre-LLM check.
		messages = lr.maybeCompactLoopContext(ctx, agent, messages, model, opts, iteration)

		// Check context after all tool results are processed to allow prompt cancellation
		// before starting the next LLM iteration.
		if err := ctx.Err(); err != nil {
			return "", iteration, err
		}
	}

	return finalContent, iteration, nil
}

// iterationMsgID appends the iteration number as a suffix to baseID for
// iterations >1 so that the frontend creates distinct assistant bubbles
// for each LLM response instead of merging them into one slot.
func iterationMsgID(baseID string, iteration int) string {
	if iteration > 1 && baseID != "" {
		return fmt.Sprintf("%s-%d", baseID, iteration)
	}
	return baseID
}

// stripImageContentParts returns a copy of messages with all image_url
// ContentParts removed. This is used when the current model does not support
// vision — historical messages may contain image data from previous turns
// (or from a different model that did support vision), and sending image
// content to a non-vision model causes API errors.
func stripImageContentParts(messages []providers.Message) []providers.Message {
	stripped := make([]providers.Message, len(messages))
	for i, msg := range messages {
		if len(msg.ContentParts) == 0 {
			stripped[i] = msg
			continue
		}
		filtered := make([]providers.ContentPart, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			if part.Type != "image_url" {
				filtered = append(filtered, part)
			}
		}
		stripped[i] = msg
		stripped[i].ContentParts = filtered
	}
	return stripped
}

// maybeCompactLoopContext runs the proactive intra-loop context compaction
// check shared by the main agent loop. It mirrors the subagent RunToolLoop
// behavior (pkg/tools/toolloop.go step 8): when the accumulated context
// exceeds the configured threshold, summarize old messages so the loop can
// keep running instead of only compacting reactively (on LLM context error)
// or at the very end (maybeSummarize).
//
// It is called from two places in runLLMIteration: BEFORE each LLM request
// (a single iteration can add hundreds of thousands of tokens of tool
// results, and without this pre-request check the request goes out
// oversized and fails) and AFTER tool execution (the original check). It
// takes the current messages slice and returns the (possibly compacted)
// slice.
func (lr *llmRunnerImpl) maybeCompactLoopContext(ctx context.Context, agent *AgentInstance, messages []providers.Message, model string, opts processOptions, iteration int) []providers.Message {
	if contextWindow := lr.al.getSessionContextWindow(opts.SessionKey); contextWindow > 0 {
		tokens := tools.EstimateLoopTokens(messages)
		thresholdPercent := lr.al.cfg().SessionCompactionThresholdPercent()
		threshold := contextWindow * thresholdPercent / 100
		if tokens > threshold {
			logger.InfoCF("agent", "Intra-loop context compaction triggered", map[string]interface{}{
				"agent_id":       agent.ID,
				"session_key":    opts.SessionKey,
				"iteration":      iteration,
				"tokens":         tokens,
				"threshold":      threshold,
				"context_window": contextWindow,
			})
			if opts.Channel == "native" {
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
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
			// Resolve the correct provider for the compaction model (which may
			// be the session model or a dedicated compaction_model). Without
			// this, the default agent.Provider is used even when the model
			// belongs to a different provider (e.g. moonshotai/kimi-k3 on an
			// Anthropic agent), causing 404 errors.
			if ref := providers.ParseModelRef(compactModel, ""); ref != nil && ref.Provider != "" {
				agentRef := providers.ParseModelRef(agent.Model, "")
				if agentRef == nil || agentRef.Provider != ref.Provider {
					if newProv, err := providers.CreateProviderForCandidate(lr.al.cfg(), ref.Provider); err == nil {
						compactProvider = newProv
					}
				}
			}
			if compacted, ok := tools.CompactLoopMessages(ctx, compactProvider, compactModel, messages, 6); ok {
				messages = compacted
				if opts.Channel == "native" {
					lr.al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: opts.Channel,
						ChatID:  opts.ChatID,
						Event:   "tool.result",
						Metadata: map[string]string{
							"tool":   "compact",
							"result": fmt.Sprintf("Tokens: ~%d → ~%d", tokens, tools.EstimateLoopTokens(messages)),
						},
					})
				}
				// Sync compaction state to the session so post-turn
				// maybeSummarize sees the reduced history and the
				// excluded messages don't bloat the next turn's context.
				if len(compacted) > 1 && strings.HasPrefix(compacted[1].Content, "[Context compacted") {
					agent.Sessions.SetSummary(opts.SessionKey, compacted[1].Content)
					agent.Sessions.ExcludeOldMessagesFromContext(opts.SessionKey, 6)
					// Increment before Save so the counter is persisted in
					// this save; incrementing after would leave the count
					// in memory until the next Save.
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
