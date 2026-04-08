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
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
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
		// Initialize verbose mode from persistent storage
		lr.al.verboseManager.InitializeFromSession(opts.SessionKey)
	}
	persistedAttachments, err := utils.PersistAttachmentsToWorkspace(agent.Workspace, opts.Attachments)
	if err != nil {
		logger.WarnCF("agent", "Failed to persist attachments to workspace", map[string]interface{}{"error": err.Error()})
		persistedAttachments = opts.Attachments
	}
	renderedUserMessage := agent.ContextBuilder.RenderUserMessage(opts.UserMessage, persistedAttachments)
	messages := agent.ContextBuilder.BuildMessages(
		history,
		summary,
		opts.UserMessage,
		persistedAttachments,
		opts.Channel,
		opts.ChatID,
	)

	// 3. Save user message to session
	agent.Sessions.AddMessage(opts.SessionKey, "user", renderedUserMessage)

	// 4. Run LLM iteration loop
	finalContent, iteration, err := lr.runLLMIteration(runCtx, agent, messages, opts)
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
	}

	// 9. Log response
	responsePreview := utils.Truncate(finalContent, 120)
	logger.InfoCF("agent", fmt.Sprintf("Response: %s", responsePreview),
		map[string]interface{}{
			"agent_id":     agent.ID,
			"session_key":  opts.SessionKey,
			"iterations":   iteration,
			"final_length": len(finalContent),
		})

	return finalContent, nil
}

// messageSignature tracks repeated LLM responses to detect loops
type messageSignature struct {
	toolCalls []toolCallSignature
	count     int
}

type toolCallSignature struct {
	name      string
	arguments string
}

// messageSignaturesEqual compares two sets of tool call signatures
func messageSignaturesEqual(a, b []toolCallSignature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].arguments != b[i].arguments {
			return false
		}
	}
	return true
}

// runLLMIteration executes the LLM call loop with tool handling.
func (lr *llmRunnerImpl) runLLMIteration(ctx context.Context, agent *AgentInstance, messages []providers.Message, opts processOptions) (string, int, error) {
	iteration := 0
	var finalContent string
	var lastMessage *messageSignature
	model := lr.modelForSession(agent, opts.SessionKey)
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

		var streamOnChunk func(chunk string, done bool)
		if opts.Channel == channels.ChannelName && opts.SendResponse {
			messageID := opts.MessageID
			if messageID == "" {
				messageID = uuid.New().String()
			}
			msgID := messageID
			streamOnChunk = func(chunk string, done bool) {
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:   opts.Channel,
					ChatID:    opts.SessionKey,
					Event:     "message.stream",
					MessageID: msgID,
					Content:   chunk,
					Metadata: map[string]string{
						"done": fmt.Sprintf("%v", done),
					},
				})
			}
		}

		callLLM := func() (*providers.LLMResponse, error) {
			// Build LLM options including reasoning config
			llmOptions := map[string]interface{}{
				"max_tokens":  agent.MaxTokens,
				"temperature": agent.Temperature,
			}

			if streamOnChunk != nil {
				if sp, ok := agent.Provider.(providers.StreamingLLMProvider); ok {
					if len(candidates) > 0 && lr.al.fallback != nil {
					} else {
						return sp.ChatStream(ctx, messages, providerToolDefs, model, llmOptions, streamOnChunk)
					}
				}
			}
			// Add reasoning config if available, with per-session override support
			sessionEffort := ""
			if opts.SessionKey != "" {
				if v, ok := lr.al.sessionThinking.Load(opts.SessionKey); ok {
					if s, ok := v.(string); ok {
						sessionEffort = s
					}
				}
			}
			if sessionEffort == "off" {
				// Explicitly disabled for this session – do not send reasoning.
			} else if sessionEffort != "" {
				// Per-session effort override.
				reasoningMap := map[string]interface{}{
					"effort": sessionEffort,
				}
				if agent.Reasoning != nil && agent.Reasoning.Summary != nil {
					reasoningMap["summary"] = *agent.Reasoning.Summary
				}
				llmOptions["reasoning"] = reasoningMap
				logger.DebugCF("agent", "Session reasoning override applied", map[string]interface{}{
					"agent_id":    agent.ID,
					"session_key": opts.SessionKey,
					"effort":      sessionEffort,
				})
			} else if agent.Reasoning != nil {
				reasoningMap := map[string]interface{}{}
				if agent.Reasoning.Effort != nil {
					reasoningMap["effort"] = *agent.Reasoning.Effort
				}
				if agent.Reasoning.Summary != nil {
					reasoningMap["summary"] = *agent.Reasoning.Summary
				}
				if len(reasoningMap) > 0 {
					llmOptions["reasoning"] = reasoningMap
					logger.DebugCF("agent", "Reasoning config applied", map[string]interface{}{
						"agent_id": agent.ID,
						"effort":   agent.Reasoning.Effort,
						"summary":  agent.Reasoning.Summary,
					})
				}
			}

			if len(candidates) > 0 && lr.al.fallback != nil {
				fbResult, fbErr := lr.al.fallback.Execute(ctx, candidates,
					func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
						// Create provider dynamically for each candidate
						providerInst, err := providers.CreateProviderForCandidate(lr.al.cfg(), provider)
						if err != nil {
							// If we can't create a provider (e.g., in tests with mock providers),
							// fall back to using the agent's provider directly
							log.Printf("[DEBUG] Failed to create provider for %s: %v", provider, err)
							if agent.Provider != nil {
								return agent.Provider.Chat(ctx, messages, providerToolDefs, model, llmOptions)
							}
							return nil, fmt.Errorf("no provider available for model %s", model)
						}
						fullModel := FormatProviderModel(provider, model)
						log.Printf("[DEBUG] Fallback attempt: provider=%s, model=%s, fullModel=%s", provider, model, fullModel)
						return providerInst.Chat(ctx, messages, providerToolDefs, fullModel, llmOptions)
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
			return agent.Provider.Chat(ctx, messages, providerToolDefs, model, llmOptions)
		}

		// Retry loop for context/token errors
		maxRetries := 2
		for retry := 0; retry <= maxRetries; retry++ {
			response, err = callLLM()
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return "", iteration, ctx.Err()
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
				waitTime := time.Duration(retry+1) * 2 * time.Second
				select {
				case <-time.After(waitTime):
				case <-ctx.Done():
					return "", iteration, ctx.Err()
				}
				continue
			}

			if isContextError && retry < maxRetries {
				logger.WarnCF("agent", "Context window error detected, attempting summarization", map[string]interface{}{
					"error": err.Error(),
					"retry": retry,
				})

				if retry == 0 && !constants.IsInternalChannel(opts.Channel) {
					lr.al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: opts.Channel,
						ChatID:  opts.ChatID,
						Content: "Context window exceeded. Summarizing history and retrying...",
					})
				}

				// Use summarizeSession instead of forceCompression to preserve context
				stats := lr.al.sessionManager.summarizeSession(agent, opts.SessionKey)
				if stats == nil {
					logger.ErrorCF("agent", "Summarization failed, falling back to compression", nil)
					lr.al.sessionManager.(*sessionManagerImpl).forceCompression(agent, opts.SessionKey)
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

		// Track token usage from response
		if opts.SessionKey != "" {
			if response.Usage != nil {
				lr.al.sessionManager.AddTokenCounts(opts.SessionKey, response.Usage.PromptTokens, response.Usage.CompletionTokens)
				logger.DebugCF("agent", "Token usage tracked", map[string]interface{}{
					"agent_id":          agent.ID,
					"session_key":       opts.SessionKey,
					"prompt_tokens":     response.Usage.PromptTokens,
					"completion_tokens": response.Usage.CompletionTokens,
					"total_tokens":      response.Usage.TotalTokens,
				})
			} else {
				// Provider returned no usage data — estimate using 2.5 chars/token heuristic
				var inputChars int
				for _, msg := range messages {
					inputChars += utf8.RuneCountInString(msg.Content)
				}
				inputEst := inputChars * 2 / 5
				outputEst := utf8.RuneCountInString(response.Content) * 2 / 5
				lr.al.sessionManager.AddTokenCounts(opts.SessionKey, inputEst, outputEst)
				logger.DebugCF("agent", "Token usage estimated (provider returned no usage data)", map[string]interface{}{
					"agent_id":    agent.ID,
					"session_key": opts.SessionKey,
					"input_est":   inputEst,
					"output_est":  outputEst,
				})
			}
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
			map[string]interface{}{"agent_id": agent.ID,
				"tools":     toolNames,
				"count":     len(response.ToolCalls),
				"iteration": iteration,
			})

		// Build signature of this message to detect loops
		currentSignature := messageSignature{
			toolCalls: make([]toolCallSignature, 0, len(response.ToolCalls)),
		}
		for _, tc := range response.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			currentSignature.toolCalls = append(currentSignature.toolCalls, toolCallSignature{
				name:      tc.Name,
				arguments: string(argsJSON),
			})
		}

		// Check if this message matches the last one (loop detection)
		if lastMessage != nil && messageSignaturesEqual(lastMessage.toolCalls, currentSignature.toolCalls) {
			lastMessage.count++
			if lastMessage.count >= 3 {
				logger.WarnCF("agent", "Detected repeated message loop, injecting guidance",
					map[string]interface{}{"agent_id": agent.ID,
						"repetitions": lastMessage.count,
						"iteration":   iteration,
						"tools":       toolNames,
					})
				// Inject guidance message to break the loop
				guidanceMsg := providers.Message{
					Role:    "user",
					Content: fmt.Sprintf("⚠️ GUIDANCE: You have sent the same tool calls multiple times consecutively. This appears to be a loop. The previous tool calls have already been executed and their results are in the conversation history. Please STOP repeating the same tool calls and either:\n1. Analyze the results you've already received, or\n2. Try a different approach, or\n3. Provide a final response based on the information gathered."),
				}
				messages = append(messages, guidanceMsg)
				agent.Sessions.AddMessage(opts.SessionKey, "user", guidanceMsg.Content)
				// Reset counter after injecting guidance
				lastMessage.count = 0
			}
		} else {
			lastMessage = &currentSignature
			lastMessage.count = 1
		}

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
			if err := ctx.Err(); err != nil {
				return "", iteration, err
			}

			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]interface{}{
					"agent_id":  agent.ID,
					"tool":      tc.Name,
					"iteration": iteration,
				})

			// Native clients consume structured tool events; other channels keep the
			// existing verbose text notifications.
			level := lr.al.verboseManager.GetLevel(opts.SessionKey)
			if opts.Channel == channels.ChannelName {
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.SessionKey,
					Event:   "tool.executing",
					Metadata: map[string]string{
						"tool":   tc.Name,
						"action": "Executing " + tc.Name,
					},
				})
			} else if level != session.VerboseOff {
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
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
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

				logger.InfoCF("agent", "Async tool completed",
					map[string]interface{}{
						"tool": tc.Name,
					})

				publishSubagentAsyncResult(lr.al, opts.SessionKey, opts.Channel, opts.ChatID, result)
			}

			// Special handling for exec tool with approval
			if tc.Name == "exec" && lr.al.approvalManager != nil {
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
					approval := lr.al.approvalManager.CreateApproval(
						opts.SessionKey,
						toolResult.ApprovalRequired.Command,
						toolResult.ApprovalRequired.Reason,
						chatIDInt,
					)

					if opts.Channel == channels.ChannelName {
						lr.al.bus.PublishOutbound(bus.OutboundMessage{
							Channel: opts.Channel,
							ChatID:  opts.SessionKey,
							Event:   "approval.request",
							Metadata: map[string]string{
								"id":      approval.ID,
								"command": toolResult.ApprovalRequired.Command,
								"reason":  toolResult.ApprovalRequired.Reason,
							},
						})
					} else {
						// Build inline keyboard
						keyboard := lr.al.approvalManager.BuildApprovalKeyboard(approval.ID)

						// Send message with keyboard
						lr.al.bus.PublishOutbound(bus.OutboundMessage{
							Channel:     opts.Channel,
							ChatID:      opts.ChatID,
							Content:     approvalMsg,
							ReplyMarkup: keyboard,
						})
					}

					// Wait for user response
					approved, err := approval.WaitForResponse(ctx, lr.al.approvalManager.GetTimeout())
					if err != nil {
						if ctx.Err() != nil {
							return "", iteration, ctx.Err()
						}
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

			if toolResult == nil {
				if err := ctx.Err(); err != nil {
					return "", iteration, err
				}
				toolResult = tools.ErrorResult(fmt.Sprintf("tool %s returned no result", tc.Name))
			}

			if err := ctx.Err(); err != nil {
				return "", iteration, err
			}

			// Native clients consume structured tool results; other channels keep the
			// existing full verbose message.
			if opts.Channel == channels.ChannelName {
				resultPreview := toolResult.ForLLM
				if resultPreview == "" && toolResult.Err != nil {
					resultPreview = toolResult.Err.Error()
				}
				if len(resultPreview) > 300 {
					resultPreview = resultPreview[:300] + "..."
				}
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.Channel,
					ChatID:  opts.SessionKey,
					Event:   "tool.result",
					Metadata: map[string]string{
						"tool":   tc.Name,
						"result": resultPreview,
					},
				})
			} else if lr.al.verboseManager.IsFull(opts.SessionKey) {
				status := "✅"
				if toolResult.IsError {
					status = "❌"
				}
				resultPreview := toolResult.ForLLM
				if len(resultPreview) > 300 {
					resultPreview = resultPreview[:300] + "..."
				}
				verboseResult := fmt.Sprintf("%s **Result:** `%s`\n```\n%s\n```", status, tc.Name, resultPreview)
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:        opts.Channel,
					ChatID:         opts.ChatID,
					Content:        verboseResult,
					IsIntermediate: true, // Don't stop typing indicator for verbose result notifications
				})
			}

			// Send ForUser content to user immediately if not Silent
			if !toolResult.Silent && toolResult.ForUser != "" && opts.SendResponse {
				lr.al.bus.PublishOutbound(bus.OutboundMessage{
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

			if len(toolResult.ContextMessages) > 0 {
				messages = append(messages, toolResult.ContextMessages...)
				for _, contextMsg := range toolResult.ContextMessages {
					agent.Sessions.AddFullMessage(opts.SessionKey, contextMsg)
				}
			}
		}
	}

	return finalContent, iteration, nil
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (lr *llmRunnerImpl) updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := agent.Tools.Get("send_file"); ok {
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

// modelForSession gets the model for a session (user-selected or agent default)
func (lr *llmRunnerImpl) modelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		if model, ok := lr.al.sessionModels.Load(sessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
			}
		}
	}
	return agent.Model
}

// formatProviderModel formats provider/model string
func (lr *llmRunnerImpl) formatProviderModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return model
	}
	if strings.HasPrefix(model, provider+"/") {
		return model
	}
	return provider + "/" + model
}
