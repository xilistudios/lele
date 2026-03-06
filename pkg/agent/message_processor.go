// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// messageProcessor is the internal interface for message processing operations.
type messageProcessor interface {
	processMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
	processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
}

// messageProcessorImpl implements the messageProcessor interface for handling
// message routing, agent resolution, and processing orchestration.
type messageProcessorImpl struct {
	al *AgentLoop
}

// newMessageProcessor creates a new message processor instance.
func newMessageProcessor(al *AgentLoop) *messageProcessorImpl {
	return &messageProcessorImpl{
		al: al,
	}
}

// processMessage is the main entry point for processing inbound messages.
func (mp *messageProcessorImpl) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return mp.processSystemMessage(ctx, msg)
	}

	// Check for commands
	if response, handled := mp.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// Route to determine agent and session key
	route := mp.al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := mp.al.registry.GetAgent(route.AgentID)
	if !ok {
		agent = mp.al.registry.GetDefaultAgent()
	}

	// Use routed session key, but honor pre-set agent-scoped keys (for ProcessDirect/cron)
	// Also honor channel-specific session keys (e.g., telegram:<chat_id>)
	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		if strings.HasPrefix(msg.SessionKey, "agent:") || strings.HasPrefix(msg.SessionKey, "telegram:") {
			sessionKey = msg.SessionKey
		}
	}

	// Check if a session-specific agent is set (e.g., via /agent command)
	if sessionAgentID := mp.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	// Keep session model in sync with the active/session-selected agent unless user
	// explicitly changed model with /model.
	if _, hasSessionModel := mp.al.sessionModels.Load(sessionKey); !hasSessionModel && agent != nil {
		if agent.Model != "" {
			mp.al.sessionModels.Store(sessionKey, agent.Model)
		} else {
			mp.al.sessionModels.Store(sessionKey, mp.al.cfg.Agents.Defaults.Model)
		}
	}

	logger.InfoCF("agent", "Routed message",
		map[string]interface{}{
			"agent_id":    agent.ID,
			"session_key": sessionKey,
			"matched_by":  route.MatchedBy,
		})

	return mp.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
}

// processSystemMessage handles messages from the system channel.
func (mp *messageProcessorImpl) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Extract reply message ID from metadata if available
	replyToMessageID := ""
	if msg.Metadata != nil {
		replyToMessageID = msg.Metadata["message_id"]
	}

	// Parse command from content
	content := msg.Content
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := parts[0]
	args := parts[1:]
	logger.InfoCF("agent", "System message content", map[string]interface{}{
		"content": content,
		"cmd":     cmd,
	})

	// Use default agent for system messages
	agent := mp.al.registry.GetDefaultAgent()

	// Use the session key from the message if available, otherwise use main session
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = routing.BuildAgentMainSessionKey(agent.ID)
	}

	// Honor session-selected agent for command/system handling as well.
	if sessionAgentID := mp.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	// Handle commands directly without LLM
	switch cmd {
	case "/status":
		response := mp.formatStatusResponse(agent, sessionKey, originChannel)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/subagents":
		response := mp.formatSubagentsResponse(args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/new":
		response := mp.handleNewCommand(agent, sessionKey)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/clear":
		if agent != nil {
			agent.Sessions.TruncateHistory(sessionKey, 0)
			agent.Sessions.SetSummary(sessionKey, "")
			agent.Sessions.Save(sessionKey)
		}
		return "", nil

	case "/stop":
		// Stop all subagents first
		subagentCount := mp.stopAllSubagents()
		// Cancel any active session processing
		mp.cancelSession(sessionKey)
		response := "Agente detenido."
		if subagentCount > 0 {
			response = fmt.Sprintf("Agente detenido (incluye %d subagente(s)).", subagentCount)
		}
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/model":
		response := mp.handleModelCommand(agent, sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/compact":
		// Manual compaction command - use existing sessionKey from caller
		history := agent.Sessions.GetHistory(sessionKey)
		if len(history) <= 4 {
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   "📭 Not enough messages to compact (need 5+).",
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		stats := mp.summarizeSession(agent, sessionKey)
		if stats == nil {
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   "❌ Compaction failed or nothing to compact.",
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		response := fmt.Sprintf("📊 Memory compacted:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
			stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
			stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/verbose":
		response := mp.handleVerboseCommand(sessionKey)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/agent":
		response := mp.handleAgentCommand(sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	// Handle subagent completion messages - show result directly to user
	case "Task":
		// Subagent task completion message - display directly to user
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   content,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil
	}

	// For non-command messages, run through LLM
	return mp.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		SendResponse:    true,
	})
}

// ProcessDirect processes a message directly without going through the message bus.
func (mp *messageProcessorImpl) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return mp.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

// ProcessDirectWithChannel processes a message directly with channel information.
func (mp *messageProcessorImpl) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return mp.processMessage(ctx, msg)
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (mp *messageProcessorImpl) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	agent := mp.al.registry.GetDefaultAgent()
	return mp.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

// stopAllSubagents stops all running subagents and returns the count of stopped tasks.
func (mp *messageProcessorImpl) stopAllSubagents() int {
	totalStopped := 0
	for _, manager := range mp.al.subagents {
		if manager != nil {
			stopped := manager.StopAll()
			totalStopped += stopped
		}
	}
	return totalStopped
}

// cancelSession cancels any active processing for a specific session
func (mp *messageProcessorImpl) cancelSession(sessionKey string) {
	if cancel, ok := mp.al.sessionCancels.Load(sessionKey); ok {
		if cf, ok := cancel.(context.CancelFunc); ok && cf != nil {
			cf()
		}
		mp.al.sessionCancels.Delete(sessionKey)
	}
}

// runAgentLoop is the core message processing logic.
func (mp *messageProcessorImpl) runAgentLoop(ctx context.Context, agent *AgentInstance, opts processOptions) (string, error) {
	// 0. Record last channel for heartbeat notifications (skip internal channels)
	if opts.Channel != "" && opts.ChatID != "" {
		// Don't record internal channels (cli, system, subagent)
		if !constants.IsInternalChannel(opts.Channel) {
			channelKey := fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID)
			if err := mp.al.RecordLastChannel(channelKey); err != nil {
				logger.WarnCF("agent", "Failed to record last channel", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// 1. Update tool contexts
	mp.updateToolContexts(agent, opts.Channel, opts.ChatID)

	// 2. Build messages (skip history for heartbeat)
	var history []providers.Message
	var summary string
	if !opts.NoHistory {
		history = agent.Sessions.GetHistory(opts.SessionKey)
		summary = agent.Sessions.GetSummary(opts.SessionKey)
		// Initialize verbose mode from persistent storage
		mp.al.verboseManager.InitializeFromSession(opts.SessionKey)
	}
	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		nil,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	agent.Sessions.AddMessage(opts.SessionKey, "user", opts.UserMessage)

	// 4. Run LLM iteration loop
	finalContent, iteration, err := mp.runLLMIteration(ctx, agent, messages, opts)
	if err != nil {
		return "", err
	}

	// If last tool had ForUser content and we already sent it, we might not need to send final response
	// This is controlled by the tool's Silent flag and ForUser content

	// 5. Handle empty response
	if finalContent == "" {
		finalContent = opts.DefaultResponse
	}

	// 6. Save final assistant message to session
	agent.Sessions.AddMessage(opts.SessionKey, "assistant", finalContent)
	agent.Sessions.Save(opts.SessionKey)

	// 7. Optional: summarization
	if opts.EnableSummary {
		mp.maybeSummarize(agent, opts.SessionKey, opts.Channel, opts.ChatID)
	}

	// 8. Optional: send response via bus
	if opts.SendResponse {
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.Channel,
			ChatID:  opts.ChatID,
			Content: finalContent,
		})
	}

	// 9. Log response
	responsePreview := truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"agent_id":     agent.ID,
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// runLLMIteration executes the LLM call loop with tool handling.
func (mp *messageProcessorImpl) runLLMIteration(ctx context.Context, agent *AgentInstance, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	model := mp.modelForSession(agent, opts.SessionKey)
	candidates := agent.Candidates
	if model != agent.Model {
		if ref := providers.ParseModelRef(model, mp.al.cfg.Agents.Defaults.Provider); ref != nil {
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
		iteration++

		logger.DebugCF("agent", "LLM iteration",
			map[string]interface{}{
				"agent_id":  agent.ID,
				"iteration": iteration,
				"max":       agent.MaxIterations,
			})

		// Build tool definitions
		providerToolDefs := agent.Tools.ToProviderDefs()

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

		// Call LLM with fallback chain if candidates are configured.
		var response *providers.LLMResponse
		var err error

		callLLM := func() (*providers.LLMResponse, error) {
			if len(candidates) > 1 && mp.al.fallback != nil {
				fbResult, fbErr := mp.al.fallback.Execute(ctx, candidates,
					func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
						// Create provider dynamically for each candidate
						providerInst, err := providers.CreateProviderForCandidate(mp.al.cfg, provider)
						if err != nil {
							return nil, fmt.Errorf("failed to create provider for %s: %w", provider, err)
						}
						fullModel := FormatProviderModel(provider, model)
						log.Printf("[DEBUG] Fallback attempt: provider=%s, model=%s, fullModel=%s", provider, model, fullModel)
						return providerInst.Chat(ctx, messages, providerToolDefs, fullModel, map[string]interface{}{
							"max_tokens":  agent.MaxTokens,
							"temperature": agent.Temperature,
						})
					},
				)
				if fbErr != nil {
					return nil, fbErr
				}
				if fbResult.Provider != "" && len(fbResult.Attempts) > 0 {
					logger.InfoCF("agent", fmt.Sprintf("Fallback: succeeded with %s/%s after %d attempts",
						fbResult.Provider, fbResult.Model, len(fbResult.Attempts)+1),
						map[string]interface{}{"agent_id": agent.ID, "iteration": iteration})
				}
				return fbResult.Response, nil
			}
			return agent.Provider.Chat(ctx, messages, providerToolDefs, model, map[string]interface{}{
				"max_tokens":  agent.MaxTokens,
				"temperature": agent.Temperature,
			})
		}

		// Retry loop for context/token errors
		maxRetries := 2
		for retry := 0; retry <= maxRetries; retry++ {
			response, err = callLLM()
			if err == nil {
				break
			}

			errMsg := strings.ToLower(err.Error())
			isContextError := strings.Contains(errMsg, "token") ||
				strings.Contains(errMsg, "invalidparameter") ||
				strings.Contains(errMsg, "length")
			isNetworkTimeout := strings.Contains(errMsg, "context deadline exceeded") ||
				strings.Contains(errMsg, "timeout") ||
				strings.Contains(errMsg, "client.timeout")

			if isNetworkTimeout {
				logger.WarnCF("agent", "Network timeout, retrying without compression", map[string]interface{}{
					"error": err.Error(),
					"retry": retry,
				})
				// Wait a bit before retrying
				time.Sleep(time.Duration(retry+1) * 2 * time.Second)
				continue
			}

			if isContextError && retry < maxRetries {
				logger.WarnCF("agent", "Context window error detected, attempting summarization", map[string]interface{}{
					"error": err.Error(),
					"retry": retry,
				})

				if retry == 0 && !constants.IsInternalChannel(opts.Channel) {
					mp.al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: opts.Channel,
						ChatID:  opts.ChatID,
						Content: "Context window exceeded. Summarizing history and retrying...",
					})
				}

				// Use summarizeSession instead of forceCompression to preserve context
				stats := mp.summarizeSession(agent, opts.SessionKey)
				if stats == nil {
					logger.ErrorCF("agent", "Summarization failed, falling back to compression", nil)
					mp.forceCompression(agent, opts.SessionKey)
				}
				newHistory := agent.Sessions.GetHistory(opts.SessionKey)
				newSummary := agent.Sessions.GetSummary(opts.SessionKey)
				messages = agent.ContextBuilder.BuildMessages(
					newHistory, newSummary, "",
					nil, opts.Channel, opts.ChatID,
				)
				continue
			}
			break
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

		// Check if no tool calls - we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("agent", "LLM response without tool calls (direct answer)",
				map[string]interface{}{
					"agent_id":      agent.ID,
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})
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
			break
		}

		// Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("agent", "LLM requested tool calls",
			map[string]interface{}{
				"agent_id":  agent.ID,
				"tools":     toolNames,
				"count":     len(response.ToolCalls),
				"iteration": iteration,
			})

		// Build assistant message with tool calls
		assistantMsg := providers.Message{
			Role:    "assistant",
			Content: response.Content,
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
				Name: tc.Name,
			})
		}
		messages = append(messages, assistantMsg)

		// Save assistant message with tool calls to session
		agent.Sessions.AddFullMessage(opts.SessionKey, assistantMsg)

		// Execute tool calls
		for _, tc := range response.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"agent_id":  agent.ID,
					"tool":      tc.Name,
					"iteration": iteration,
				})

			// Verbose mode: send notification before executing tool
			level := mp.al.verboseManager.GetLevel(opts.SessionKey)
			if level != session.VerboseOff {
				var verboseMsg string
				if level == session.VerboseFull {
					// Full mode: detailed tool call with JSON args
					verboseMsg = fmt.Sprintf("🔧 **Tool Call (%d):** `%s`", iteration, tc.Name)
					if argsPreview != "" && argsPreview != "{}" {
						verboseMsg += fmt.Sprintf("\n```json\n%s\n```", argsPreview)
					}
				} else {
					// Basic mode: simplified description
					verboseMsg = formatBasicToolMessage(tc.Name, tc.Arguments)
				}
				mp.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:        opts.Channel,
					ChatID:         opts.ChatID,
					Content:        verboseMsg,
					IsIntermediate: true, // Don't stop typing indicator for verbose notifications
				})
			}

			var toolResult *tools.ToolResult

			// Create async callback for tools that implement AsyncTool
			// Async completion is routed back as a system inbound event so the
			// parent agent loop can notify the original chat reliably.
			asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
				if result == nil {
					return
				}

				if !result.Silent && result.ForUser != "" {
					logger.InfoCF("agent", "Async tool completed",
						map[string]interface{}{
							"tool":        tc.Name,
							"content_len": len(result.ForUser),
						})
				}

				if mp.al.bus != nil && strings.TrimSpace(result.ForUser) != "" {
					mp.al.bus.PublishInbound(bus.InboundMessage{
						Channel:    "system",
						SenderID:   "subagent",
						ChatID:     fmt.Sprintf("%s:%s", opts.Channel, opts.ChatID),
						Content:    result.ForUser,
						SessionKey: opts.SessionKey,
					})
				}
			}

			// Special handling for exec tool with approval
			if tc.Name == "exec" && mp.al.approvalManager != nil {
				toolResult = agent.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID, asyncCallback)

				// Check if approval is required
				if toolResult.ApprovalRequired != nil {
					// Send approval request to user
					approvalMsg := fmt.Sprintf("⚠️ **Se requiere aprobación**\n\n"+
						"El siguiente comando puede ser peligroso:\n"+
						"`%s`\n\n"+
						"Razón: %s",
						toolResult.ApprovalRequired.Command,
						toolResult.ApprovalRequired.Reason)

					// Parse chatID as int64 for approval manager
					var chatIDInt int64
					fmt.Sscanf(opts.ChatID, "%d", &chatIDInt)

					// Create approval request
					approval := mp.al.approvalManager.CreateApproval(
						opts.SessionKey,
						toolResult.ApprovalRequired.Command,
						toolResult.ApprovalRequired.Reason,
						chatIDInt,
					)

					// Build inline keyboard
					keyboard := mp.al.approvalManager.BuildApprovalKeyboard(approval.ID)

					// Send message with keyboard
					mp.al.bus.PublishOutbound(bus.OutboundMessage{
						Channel:     opts.Channel,
						ChatID:      opts.ChatID,
						Content:     approvalMsg,
						ReplyMarkup: keyboard,
					})

					// Wait for user response
					approved, err := approval.WaitForResponse(mp.al.approvalManager.GetTimeout())
					if err != nil {
						toolResult = &tools.ToolResult{
							IsError: true,
							ForLLM:  "Error: timeout esperando aprobación del usuario",
						}
					} else if approved {
						// User approved - execute the command directly
						// We need to get the exec tool and set it to bypass guard
						if execTool, ok := agent.Tools.Get("exec"); ok {
							if et, ok := execTool.(*tools.ExecTool); ok {
								// Temporarily bypass all security guards for approved command
								et.SetBypassGuard(true)
								toolResult = et.Execute(ctx, tc.Arguments)
								// Re-enable approval mode
								et.SetBypassGuard(false)
							}
						}
						// If tool execution failed or tool not found, use error result
						if toolResult == nil {
							toolResult = tools.ErrorResult("Failed to execute approved command")
						}
					} else {
						// User rejected
						toolResult = &tools.ToolResult{
							IsError: true,
							ForLLM:  "El comando fue rechazado por el usuario por razones de seguridad.",
						}
					}
				}
			} else {
				toolResult = agent.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, opts.Channel, opts.ChatID, asyncCallback)
			}

			// Verbose mode: send result notification (only in Full mode)
			if mp.al.verboseManager.IsFull(opts.SessionKey) {
				status := "✅"
				if toolResult.IsError {
					status = "❌"
				}
				resultPreview := toolResult.ForLLM
				if len(resultPreview) > 300 {
					resultPreview = resultPreview[:300] + "..."
				}
				verboseResult := fmt.Sprintf("%s **Result:** `%s`\n```\n%s\n```", status, tc.Name, resultPreview)
				mp.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:        opts.Channel,
					ChatID:         opts.ChatID,
					Content:        verboseResult,
					IsIntermediate: true, // Don't stop typing indicator for verbose result notifications
				})
			}

			// Send ForUser content to user immediately if not Silent
			if !toolResult.Silent && toolResult.ForUser != "" && opts.SendResponse {
				mp.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.ChatID,
					Content: toolResult.ForUser,
				})
				logger.DebugCF("agent", "Sent tool result to user",
					map[string]interface{}{
						"tool":        tc.Name,
						"content_len": len(toolResult.ForUser),
					})
			}

			// Determine content for LLM based on tool result
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Save tool result message to session
			agent.Sessions.AddFullMessage(opts.SessionKey, toolResultMsg)
		}
	}

	return finalContent, iteration, nil
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (mp *messageProcessorImpl) updateToolContexts(agent *AgentInstance, channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := agent.Tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// Returns statistics about the compaction if it was triggered.
func (mp *messageProcessorImpl) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := mp.estimateTokens(newHistory)
	threshold := agent.ContextWindow * 75 / 100

	// Only trigger based on token estimate, not message count
	if tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := mp.al.summarizing.LoadOrStore(summarizeKey, true); !loading {
			stats := mp.summarizeSession(agent, sessionKey)
			mp.al.summarizing.Delete(summarizeKey)

			if !constants.IsInternalChannel(channel) && stats != nil {
				mp.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: channel,
					ChatID:  chatID,
					Content: fmt.Sprintf("📊 Memory optimized:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
						stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
						stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens),
				})
			}
			return stats
		}
	}
	return nil
}

// summarizeSession summarizes the conversation history for a session.
// Passes ALL old messages to the LLM with instructions to create a comprehensive summary.
// Returns statistics about the operation.
func (mp *messageProcessorImpl) summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)

	// Need at least system prompt + 3 messages to summarize (keep last 2 exchanges)
	if len(history) <= 3 {
		return nil
	}

	// Calculate before stats
	beforeMessages := len(history)
	beforeTokens := mp.estimateTokens(history)

	// Keep system prompt [0] and last 2 messages for continuity
	toSummarize := history[1 : len(history)-2] // Everything between system and last 2

	if len(toSummarize) == 0 {
		return nil
	}

	// Build comprehensive summary prompt with ALL old messages
	prompt := "Please provide a comprehensive summary of the following conversation. " +
		"Capture all important context, decisions, facts, and action items so that " +
		"someone reading just this summary would understand what happened.\n\n"

	if existingSummary != "" {
		prompt += "=== PREVIOUS SUMMARY ===\n" + existingSummary + "\n\n"
	}

	prompt += "=== CONVERSATION TO SUMMARIZE ===\n"
	for _, m := range toSummarize {
		role := strings.ToUpper(m.Role)
		content := m.Content
		// Truncate very long messages for the summary prompt
		if len(content) > 4000 {
			content = content[:4000] + "\n[Content truncated...]"
		}
		prompt += fmt.Sprintf("%s: %s\n\n", role, content)
	}

	prompt += "=== END OF CONVERSATION ===\n\n" +
		"Now provide a detailed summary that preserves all critical context."

	// Call LLM to summarize everything
	resp, err := agent.Provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, agent.Model, map[string]interface{}{
		"max_tokens":  2048,
		"temperature": 0.3,
	})

	var finalSummary string
	if err == nil && resp != nil {
		finalSummary = resp.Content
	} else if existingSummary != "" {
		// Fall back to existing summary
		finalSummary = existingSummary + "\n[Update: Additional conversation not summarized due to error]"
	}

	if finalSummary == "" {
		return nil
	}

	agent.Sessions.SetSummary(sessionKey, finalSummary)
	agent.Sessions.TruncateHistory(sessionKey, 4)
	agent.Sessions.Save(sessionKey)

	// Calculate after stats
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	afterMessages := len(afterHistory)
	afterTokens := mp.estimateTokens(afterHistory)

	return &SummarizeStats{
		BeforeMessages:  beforeMessages,
		AfterMessages:   afterMessages,
		DroppedMessages: beforeMessages - afterMessages,
		BeforeTokens:    beforeTokens,
		AfterTokens:     afterTokens,
		SavedTokens:     beforeTokens - afterTokens,
	}
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest 50% of messages (keeping system prompt and last user message).
func (mp *messageProcessorImpl) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// Keep system prompt (usually [0]) and the very last message (user's trigger)
	// We want to drop the oldest half of the *conversation*
	// Assuming [0] is system, [1:] is conversation
	conversation := history[1 : len(history)-1]
	if len(conversation) == 0 {
		return
	}

	// Helper to find the mid-point of the conversation
	mid := len(conversation) / 2

	droppedCount := mid
	keptConversation := conversation[mid:]

	newHistory := make([]providers.Message, 0)
	newHistory = append(newHistory, history[0]) // System prompt

	// Add a note about compression
	compressionNote := fmt.Sprintf("[System: Emergency compression dropped %d oldest messages due to context limit]", droppedCount)

	// We only modify the messages list here
	newHistory = append(newHistory, providers.Message{
		Role:    "system",
		Content: compressionNote,
	})

	newHistory = append(newHistory, keptConversation...)
	newHistory = append(newHistory, history[len(history)-1]) // Last message

	// Update session
	agent.Sessions.SetHistory(sessionKey, newHistory)
	agent.Sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]interface{}{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(newHistory),
	})
}

// estimateTokens estimates the number of tokens in a message list.
func (mp *messageProcessorImpl) estimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}

// handleCommand handles slash commands in user messages.
func (mp *messageProcessorImpl) handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return "", false
	}

	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", false
	}

	cmd := parts[0]
	args := parts[1:]
	route := mp.al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := mp.al.registry.GetAgent(route.AgentID)
	if !ok {
		agent = mp.al.registry.GetDefaultAgent()
	}
	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		if strings.HasPrefix(msg.SessionKey, "agent:") || strings.HasPrefix(msg.SessionKey, "telegram:") {
			sessionKey = msg.SessionKey
		}
	}
	if sessionAgentID := mp.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	switch cmd {
	case "/new":
		return mp.handleNewCommand(agent, sessionKey), true
	case "/clear":
		if agent != nil {
			agent.Sessions.TruncateHistory(sessionKey, 0)
			agent.Sessions.SetSummary(sessionKey, "")
			agent.Sessions.Save(sessionKey)
		}
		return "✅ Conversation cleared.", true
	case "/status":
		return mp.formatStatusResponse(agent, sessionKey, msg.Channel), true
	case "/model":
		return mp.handleModelCommand(agent, sessionKey, args), true
	case "/verbose":
		return mp.handleVerboseCommand(sessionKey), true
	case "/agent":
		return mp.handleAgentCommand(sessionKey, args), true
	case "/subagents":
		return mp.formatSubagentsResponse(args), true
	case "/stop":
		subagentCount := mp.stopAllSubagents()
		mp.cancelSession(sessionKey)
		if subagentCount > 0 {
			return fmt.Sprintf("Agente detenido (incluye %d subagente(s)).", subagentCount), true
		}
		return "Agente detenido.", true
	case "/show":
		if len(args) < 1 {
			return "Usage: /show [model|channel|agents]", true
		}
		switch args[0] {
		case "model":
			defaultAgent := mp.al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			return fmt.Sprintf("Current model: %s", defaultAgent.Model), true
		case "channel":
			return fmt.Sprintf("Current channel: %s", msg.Channel), true
		case "agents":
			agentIDs := mp.al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown show target: %s", args[0]), true
		}
	case "/list":
		if len(args) < 1 {
			return "Usage: /list [models|channels|agents]", true
		}
		switch args[0] {
		case "models":
			return "Available models: configured in config.json per agent", true
		case "channels":
			if mp.al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			channels := mp.al.channelManager.GetEnabledChannels()
			if len(channels) == 0 {
				return "No channels enabled", true
			}
			return fmt.Sprintf("Enabled channels: %s", strings.Join(channels, ", ")), true
		case "agents":
			agentIDs := mp.al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown list target: %s", args[0]), true
		}
	case "/switch":
		if len(args) < 3 || args[1] != "to" {
			return "Usage: /switch [model|channel] to <name>", true
		}
		target := args[0]
		value := args[2]
		switch target {
		case "model":
			defaultAgent := mp.al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			oldModel := defaultAgent.Model
			defaultAgent.Model = value
			return fmt.Sprintf("Switched model from %s to %s", oldModel, value), true
		case "channel":
			if mp.al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			if _, exists := mp.al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Sprintf("Channel '%s' not found or not enabled", value), true
			}
			return fmt.Sprintf("Switched target channel to %s", value), true
		default:
			return fmt.Sprintf("Unknown switch target: %s", target), true
		}
	case "/compact":
		if agent == nil {
			return "No agent available for compaction", true
		}
		history := agent.Sessions.GetHistory(sessionKey)
		if len(history) <= 4 {
			return "📭 Not enough messages to compact (need 5+).", true
		}
		stats := mp.summarizeSession(agent, sessionKey)
		if stats == nil {
			return "❌ Compaction failed or nothing to compact.", true
		}
		return fmt.Sprintf("📊 Memory compacted:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
			stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
			stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens), true
	}

	return "", false
}

// modelForSession returns the model to use for a session.
func (mp *messageProcessorImpl) modelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		if model, ok := mp.al.sessionModels.Load(sessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
			}
		}
	}
	return agent.Model
}

// formatStatusResponse formats the status response for a session.
func (mp *messageProcessorImpl) formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.modelForSession(agent, sessionKey)
	providerName := mp.al.cfg.Agents.Defaults.Provider
	if idx := strings.Index(currentModel, "/"); idx > 0 {
		providerName = currentModel[:idx]
	}
	apiKey := ""
	if provider, ok := mp.al.cfg.Providers.GetNamed(providerName); ok {
		apiKey = provider.APIKey
		if len(apiKey) > 10 {
			apiKey = apiKey[:6] + "…" + apiKey[len(apiKey)-4:]
		}
	}
	history := agent.Sessions.GetHistory(sessionKey)
	tokenIn := mp.estimateTokens(history)
	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	contextPercent := tokenIn * 100 / contextWindow
	if contextPercent > 100 {
		contextPercent = 100
	}
	return fmt.Sprintf("🦞 picoclaw %s\nGateway version: %s\n🧠 Model: %s · 🔑 api-key %s\n🧮 Tokens: ~%d in\n📚 Context: ~%d/%d (%d%%)\n🧵 Session: %s\n⚙️ Runtime: %s · Think: %s",
		gatewayVersion(), gatewayVersion(), currentModel, apiKey, tokenIn, tokenIn, contextWindow, contextPercent, sessionKey, originChannel, "medium")
}

// formatSubagentsResponse formats the subagents list response.
func (mp *messageProcessorImpl) formatSubagentsResponse(args []string) string {
	if len(args) >= 2 && args[0] == "info" {
		task, ok := mp.getSubagentTask(args[1])
		if !ok {
			return fmt.Sprintf("Subagent task not found: %s", args[1])
		}
		return fmt.Sprintf("Task %s\nStatus: %s\nAgent: %s\nLabel: %s", task.ID, task.Status, task.AgentID, task.Label)
	}
	if len(args) >= 2 && args[0] == "stop" {
		if mp.stopSubagentTask(args[1]) {
			return fmt.Sprintf("Stopping subagent task: %s", args[1])
		}
		return fmt.Sprintf("Subagent task not running: %s", args[1])
	}
	running := mp.listRunningSubagentTasks()
	if len(running) == 0 {
		return "No running subagents.\nUse /subagents info <task_id> or /subagents stop <task_id>."
	}
	lines := make([]string, 0, len(running))
	for _, task := range running {
		lines = append(lines, fmt.Sprintf("- %s (%s)", task.ID, task.Label))
	}
	return fmt.Sprintf("Running subagents:\n%s\nUse /subagents info <task_id> or /subagents stop <task_id>.", strings.Join(lines, "\n"))
}

// handleNewCommand handles the /new command.
func (mp *messageProcessorImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	previousHistory := agent.Sessions.GetHistory(sessionKey)
	previousSummary := agent.Sessions.GetSummary(sessionKey)
	agent.Sessions.TruncateHistory(sessionKey, 0)
	agent.Sessions.SetSummary(sessionKey, "")
	// Reset memory context to ensure fresh reload of MEMORY.md and daily notes
	agent.ContextBuilder.ResetMemoryContext()
	if err := agent.Sessions.Save(sessionKey); err != nil {
		agent.Sessions.SetHistory(sessionKey, previousHistory)
		agent.Sessions.SetSummary(sessionKey, previousSummary)
		logger.WarnCF("agent", "Failed to save cleared session", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return fmt.Sprintf("Conversation cleared, but failed to persist session state: %v", err)
	}
	return "🔄 New conversation started. Context refreshed from SOUL.md, AGENTS.md, and MEMORY.md."
}

// handleModelCommand handles the /model command.
func (mp *messageProcessorImpl) handleModelCommand(agent *AgentInstance, sessionKey string, args []string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.modelForSession(agent, sessionKey)
	if len(args) == 0 {
		var models []string
		if provider, ok := mp.al.cfg.Providers.GetNamed(mp.al.cfg.Agents.Defaults.Provider); ok {
			models = make([]string, 0, len(provider.Models))
			for alias := range provider.Models {
				models = append(models, alias)
			}
			// Sort for consistent output
			for i := 0; i < len(models)-1; i++ {
				for j := i + 1; j < len(models); j++ {
					if models[i] > models[j] {
						models[i], models[j] = models[j], models[i]
					}
				}
			}
		}
		if len(models) == 0 {
			return fmt.Sprintf("Current model: %s", currentModel)
		}
		return fmt.Sprintf("Current model: %s\nAvailable models: %s\nUse /model <name> to change.", currentModel, strings.Join(models, ", "))
	}
	next := mp.al.cfg.Providers.ResolveModelAlias(args[0], mp.al.cfg.Agents.Defaults.Provider)
	if sessionKey == "" {
		return "Model switching requires a session context. Please start a conversation first."
	}
	mp.al.sessionModels.Store(sessionKey, next)
	return fmt.Sprintf("Model changed for this chat: %s -> %s", currentModel, next)
}

// handleVerboseCommand handles the /verbose command.
func (mp *messageProcessorImpl) handleVerboseCommand(sessionKey string) string {
	if sessionKey == "" {
		return "Verbose mode requires a session context. Please start a conversation first."
	}
	newLevel := mp.al.verboseManager.CycleLevel(sessionKey)
	switch newLevel {
	case session.VerboseOff:
		return "🔇 Verbose mode **OFF**\nTool execution notifications are hidden."
	case session.VerboseBasic:
		return "🛠️ Verbose mode **BASIC**\nYou will see simplified tool execution notifications."
	case session.VerboseFull:
		return "📋 Verbose mode **FULL**\nYou will see detailed tool execution and results."
	}
	return "Unknown verbose level"
}

// handleAgentCommand handles the /agent command.
func (mp *messageProcessorImpl) handleAgentCommand(sessionKey string, args []string) string {
	if sessionKey == "" {
		return "Agent switching requires a session context. Please start a conversation first."
	}

	// List available agents if no argument provided
	if len(args) == 0 {
		agentList := mp.al.registry.ListAgentIDs()
		if len(agentList) == 0 {
			return "No agents configured."
		}

		var lines []string
		lines = append(lines, "🤖 Available agents:")
		for _, id := range agentList {
			if agent, ok := mp.al.registry.GetAgent(id); ok {
				name := agent.Name
				if name == "" {
					name = id
				}
				lines = append(lines, fmt.Sprintf("- %s (%s)", id, name))
			}
		}
		lines = append(lines, "")
		lines = append(lines, "Use /agent <agent_id> to switch.")
		return strings.Join(lines, "\n")
	}

	agentID := args[0]

	// Validate agent exists
	agent, ok := mp.al.registry.GetAgent(agentID)
	if !ok {
		return fmt.Sprintf("❌ Agent not found: %s", agentID)
	}

	// Get agent model
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = mp.al.cfg.Agents.Defaults.Model
	}

	// Store selected agent and its model for this session
	mp.al.sessionAgents.Store(sessionKey, agentID)
	mp.al.sessionModels.Store(sessionKey, agentModel)

	agentName := agent.Name
	if agentName == "" {
		agentName = agentID
	}

	return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s", agentName, agentModel)
}

// listRunningSubagentTasks lists all running subagent tasks.
func (mp *messageProcessorImpl) listRunningSubagentTasks() []*tools.SubagentTask {
	tasks := make([]*tools.SubagentTask, 0)
	for _, manager := range mp.al.subagents {
		for _, task := range manager.ListTasks() {
			if task.Status == "running" {
				tasks = append(tasks, task)
			}
		}
	}
	return tasks
}

// getSubagentTask gets a specific subagent task by ID.
func (mp *messageProcessorImpl) getSubagentTask(taskID string) (*tools.SubagentTask, bool) {
	for _, manager := range mp.al.subagents {
		if task, ok := manager.GetTask(taskID); ok {
			return task, true
		}
	}
	return nil, false
}

// stopSubagentTask stops a specific subagent task.
func (mp *messageProcessorImpl) stopSubagentTask(taskID string) bool {
	for _, manager := range mp.al.subagents {
		if manager.StopTask(taskID) {
			return true
		}
	}
	return false
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
