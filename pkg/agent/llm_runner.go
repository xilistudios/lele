// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
}

// llmRunnerImpl implements the llmRunner interface
type llmRunnerImpl struct {
	al *AgentLoop
}

// newLLMRunner creates a new LLM runner
func newLLMRunner(al *AgentLoop) *llmRunnerImpl {
	return &llmRunnerImpl{al: al}
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
		runCtx = sessionCtx
		defer lr.al.registerSessionCancel(opts.SessionKey, cancel)()
	}

	// 1. Update tool contexts
	lr.al.toolCoordinator.updateToolContexts(agent, opts.Channel, opts.ChatID, opts.SessionKey)

	// 2. Build messages (skip history for heartbeat)
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = agent.Sessions.GetHistory(opts.SessionKey)
		summary = agent.Sessions.GetSummary(opts.SessionKey)
		history = ensureSummaryMaterialized(agent, opts.SessionKey, history, summary)
		// Initialize verbose mode from persistent storage
		lr.al.verboseManager.InitializeFromSession(opts.SessionKey)
	}
	persistedAttachments, err := utils.PersistAttachmentsToWorkspace(agent.Workspace, opts.Attachments)
	if err != nil {
		logger.WarnCF("agent", "Failed to persist attachments to workspace", map[string]interface{}{"error": err.Error()})
		persistedAttachments = opts.Attachments
	}
	renderedUserMessage := agent.ContextBuilder.BuildCurrentUserMessage(opts.UserMessage, persistedAttachments, opts.Channel, opts.ChatID)
	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		persistedAttachments,
		opts.Channel,
		opts.ChatID,
		opts.SessionKey,
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
	if err != nil {
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
		outboundMsg := bus.OutboundMessage{
			Channel:   opts.Channel,
			ChatID:    opts.ChatID,
			Content:   finalContent,
			MessageID: opts.MessageID,
		}
		if opts.ReplyTo != "" {
			outboundMsg.ReplyTo = opts.ReplyTo
		}
		lr.al.bus.PublishOutbound(outboundMsg)
		// Return empty string to prevent duplicate publish in loop.go
		return "", nil
	}

	return finalContent, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
func (lr *llmRunnerImpl) runLLMIteration(ctx context.Context, agent *AgentInstance, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	loopDetector := newLoopDetector()
	model := lr.al.sessionManager.ModelForSession(agent, opts.SessionKey)
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

	for iteration < agent.MaxIterations {
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

		// Filter out read_image tool if the current model doesn't support vision
		modelHasVision := getSupportsImages(lr.al.cfg(), model, extractProviderFromModel(model, lr.al.cfg().Agents.Defaults.Provider))
		if !modelHasVision {
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, def := range providerToolDefs {
				if def.Function.Name != "read_image" {
					filtered = append(filtered, def)
				}
			}
			providerToolDefs = filtered
		}

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

		// Setup streaming handlers for native channel
		var streamOnChunk func(chunk string, done bool)
		var streamOnReasoning func(reasoningChunk string)
		streamer := newStreamHandler(lr.al.bus, opts.Channel, opts.SessionKey, opts.MessageID)
		if streamer.shouldStream(opts.SendResponse) {
			streamOnChunk = streamer.onChunk
			streamOnReasoning = streamer.onReasoning
		}

		// Call LLM using llmCaller with retry logic
		llmCallerInstance := newLLMCaller(lr.al)
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
		response, messages, err = llmCallerInstance.executeWithRetry(callOpts, messages)

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

			// Save assistant message with reasoning content (important for thinking models like DeepSeek)
			assistantMsg := providers.Message{
				Role:             "assistant",
				Content:          response.Content,
				ReasoningContent: response.ReasoningContent,
			}
			messages = append(messages, assistantMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

			// If response is empty, retry by prompting the model again
			if len(strings.TrimSpace(finalContent)) == 0 && iteration < agent.MaxIterations-2 {
				logger.WarnCF("agent", "Empty response received, retrying with follow-up prompt",
					map[string]interface{}{
						"agent_id":  agent.ID,
						"iteration": iteration,
					})
				messages = append(messages, providers.Message{
					Role:    "user",
					Content: "Your previous response was empty. Please provide a helpful response to my request.",
				})
				continue
			}

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
		for _, tc := range response.ToolCalls {
			toolResult, execErr := executor.Execute(toolExecOptions{
				ctx:          ctx,
				agent:        agent,
				tc:           tc,
				channel:      opts.Channel,
				chatID:       opts.ChatID,
				sessionKey:   opts.SessionKey,
				iteration:    iteration,
				sendResponse: opts.SendResponse,
			})

			if execErr != nil {
				return "", iteration, execErr
			}

			execResults = append(execResults, toolExecResult{tc: tc, res: toolResult})
		}

		// Phase 2: Append all tool result messages (role: "tool") in order
		var allContextMsgs []providers.Message
		for _, er := range execResults {
			contentForLLM := buildToolResultContent(er.res)
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
		for _, ctxMsg := range allContextMsgs {
			messages = append(messages, ctxMsg)
			agent.Sessions.AddFullMessage(opts.SessionKey, ctxMsg)
		}
	}

	return finalContent, iteration, nil
}
