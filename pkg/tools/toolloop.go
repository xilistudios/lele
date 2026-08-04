// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/utils"
)

// VerboseCallback is called when verbose mode is enabled to notify about tool execution progress.
type VerboseCallback func(iteration int, toolName string, args map[string]interface{}, result *ToolResult)

type SessionRecorder interface {
	AddFullMessage(sessionKey string, msg providers.Message)
	Save(sessionKey string) error
}

// ToolLoopConfig configures the tool execution loop.
type ToolLoopConfig struct {
	Provider        providers.LLMProvider
	Model           string
	Tools           *ToolRegistry
	MaxIterations   int
	LLMOptions      map[string]any
	VerboseCallback VerboseCallback
	SessionRecorder SessionRecorder
	SessionKey      string
	Retry           *RetryConfig    // Retry config for LLM calls. nil = no retry.
	ContextWindow   int             // Max tokens for context. 0 = no compaction.
	MessageBus      *bus.MessageBus // Optional: publish real-time events to TUI.
	Channel         string          // Origin channel for events.
	ChatID          string          // Origin chatID for events (subagent sessionKey).
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
}

// EstimateLoopTokens estimates the number of tokens in a message list.
// Uses 2.5 chars per token heuristic (same as session manager).
func EstimateLoopTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += utf8.RuneCountInString(m.Content)
		totalChars += utf8.RuneCountInString(m.ReasoningContent)
		for _, tc := range m.ToolCalls {
			if tc.Function != nil {
				totalChars += utf8.RuneCountInString(tc.Function.Arguments)
			}
		}
	}
	return totalChars * 2 / 5
}

// CompactLoopMessages reduces context size by summarizing old tool interactions.
// Keeps the system prompt (first message) and the last keepLast messages.
// Everything in between is summarized via LLM and replaced with a single message.
func CompactLoopMessages(ctx context.Context, provider providers.LLMProvider, model string, messages []providers.Message, keepLast int) ([]providers.Message, bool) {
	if len(messages) <= keepLast+2 {
		return messages, false
	}

	systemMsg := messages[0]
	tail := messages[len(messages)-keepLast:]
	middle := messages[1 : len(messages)-keepLast]

	// Build summary prompt from middle messages.
	// Use text-only content so multimodal messages (e.g. from read_image)
	// never leak base64 image data into the summarization prompt. The
	// compaction model may not support vision.
	var parts []string
	for _, m := range middle {
		content := m.TextOnlyContent()
		switch m.Role {
		case "assistant":
			if content != "" {
				c := content
				if len(c) > 500 {
					c = c[:500] + "..."
				}
				parts = append(parts, "Assistant: "+c)
			}
			for _, tc := range m.ToolCalls {
				args := ""
				if tc.Function != nil {
					args = tc.Function.Arguments
					if len(args) > 200 {
						args = args[:200] + "..."
					}
				}
				parts = append(parts, fmt.Sprintf("Tool call: %s(%s)", tc.Name, args))
			}
		case "tool":
			c := content
			if len(c) > 300 {
				c = c[:300] + "..."
			}
			parts = append(parts, "Tool result: "+c)
		case "user":
			c := content
			if len(c) > 500 {
				c = c[:500] + "..."
			}
			parts = append(parts, "User: "+c)
		}
	}

	summaryInput := strings.Join(parts, "\n")
	prompt := "You are summarizing the conversation history of an AI agent that is in the MIDDLE of executing a multi-step task using tools.\n\n" +
		"Summarize concisely but MUST preserve:\n" +
		"- The original task/goal the agent is working on\n" +
		"- What steps have been completed so far\n" +
		"- What steps remain to be done\n" +
		"- All key facts: file paths, function names, decisions, errors encountered\n" +
		"- The current state of work (what was the agent doing right before this summary)\n\n" +
		"Format as a structured summary. Do NOT conclude or wrap up — the task is still in progress.\n\n" +
		summaryInput

	apiModel := providers.StripProviderPrefix(model)
	resp, err := provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})

	if err != nil || resp == nil || resp.Content == "" {
		logger.WarnCF("toolloop", "Context compaction summarization failed, skipping", map[string]any{
			"error": fmt.Sprintf("%v", err),
		})
		return messages, false
	}

	summaryMsg := providers.Message{
		Role:    "user",
		Content: "[Context compacted — summary of previous " + fmt.Sprintf("%d", len(middle)) + " messages]\n" + resp.Content,
	}

	// Nudge the LLM to continue the in-progress task after compaction.
	continueMsg := providers.Message{
		Role:    "user",
		Content: "[The context was compacted to save space. You were in the middle of a multi-step task. Review the summary above and the recent tool results below, then CONTINUE executing the task using the available tools. Do not stop or ask for confirmation — resume where you left off.]",
	}

	compacted := append([]providers.Message{systemMsg, summaryMsg, continueMsg}, tail...)
	logger.InfoCF("toolloop", "Context compacted", map[string]any{
		"before_messages": len(messages),
		"after_messages":  len(compacted),
		"before_tokens":   EstimateLoopTokens(messages),
		"after_tokens":    EstimateLoopTokens(compacted),
	})
	return compacted, true
}

// RunToolLoop executes the LLM + tool call iteration loop.
// This is the core agent logic that can be reused by both main agent and subagents.
func RunToolLoop(ctx context.Context, config ToolLoopConfig, messages []providers.Message, channel, chatID string) (*ToolLoopResult, error) {
	iteration := 0
	var finalContent string

	if config.SessionRecorder != nil && config.SessionKey != "" {
		for _, m := range messages {
			if m.Role == "user" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, m)
				break
			}
		}
	}

	for iteration < config.MaxIterations {
		iteration++

		logger.DebugCF("toolloop", "LLM iteration",
			map[string]any{
				"iteration": iteration,
				"max":       config.MaxIterations,
			})

		// 1. Build tool definitions
		var providerToolDefs []providers.ToolDefinition
		if config.Tools != nil {
			providerToolDefs = config.Tools.ToProviderDefs()
		}

		// 2. Set default LLM options
		llmOpts := config.LLMOptions
		if llmOpts == nil {
			llmOpts = map[string]any{}
		}
		// 3. Call LLM
		// Strip provider prefix from model for API calls.
		// The provider prefix (e.g., "openrouter:") is for internal routing only
		// and must not be sent to the external LLM API.
		apiModel := providers.StripProviderPrefix(config.Model)
		var response *providers.LLMResponse
		var err error
		if config.Retry != nil {
			response, err = ChatWithRetry(ctx, config.Provider, messages, providerToolDefs, apiModel, llmOpts, *config.Retry)
		} else {
			response, err = config.Provider.Chat(ctx, messages, providerToolDefs, apiModel, llmOpts)
		}
		if err != nil {
			logger.ErrorCF("toolloop", "LLM call failed",
				map[string]any{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		// 4. If no tool calls, we're done
		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("toolloop", "LLM response without tool calls (direct answer)",
				map[string]any{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})

			// Save assistant message with reasoning content (important for thinking models)
			assistantMsg := providers.Message{
				Role:             "assistant",
				Content:          response.Content,
				ReasoningContent: response.ReasoningContent,
			}
			if config.SessionRecorder != nil && config.SessionKey != "" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, assistantMsg)
			}

			// Publish final response as stream event if bus is available
			if config.MessageBus != nil && finalContent != "" {
				config.MessageBus.PublishOutbound(bus.OutboundMessage{
					Event:   "message.stream",
					ChatID:  config.ChatID,
					Content: finalContent,
				})
			}
			break
		}
		// 5. Log tool calls
		toolNames := make([]string, 0, len(response.ToolCalls))
		for _, tc := range response.ToolCalls {
			toolNames = append(toolNames, tc.Name)
		}
		logger.InfoCF("toolloop", "LLM requested tool calls",
			map[string]any{
				"tools":     toolNames,
				"count":     len(response.ToolCalls),
				"iteration": iteration,
			})

		// 6. Build assistant message with tool calls
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
				Name: tc.Name,
			})
		}
		messages = append(messages, assistantMsg)

		if config.SessionRecorder != nil && config.SessionKey != "" {
			config.SessionRecorder.AddFullMessage(config.SessionKey, assistantMsg)
		}

		// 7. Execute tool calls
		for _, tc := range response.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("toolloop", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]any{
					"tool":      tc.Name,
					"iteration": iteration,
				})

			// Execute tool (no async callback for subagents - they run independently)
			var toolResult *ToolResult

			// Publish tool.executing event if bus is available
			if config.MessageBus != nil {
				action := tc.Name
				if argsPreview != "" {
					action = fmt.Sprintf("%s(%s)", tc.Name, argsPreview)
				}
				config.MessageBus.PublishOutbound(bus.OutboundMessage{
					Event:   "tool.executing",
					ChatID:  config.ChatID,
					Content: "",
					Metadata: map[string]string{
						"tool":      tc.Name,
						"action":    action,
						"arguments": string(argsJSON),
					},
				})
			}

			if config.Tools != nil {
				toolResult = config.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, channel, chatID, nil)
			} else {
				toolResult = ErrorResult("No tools available")
			}

			if toolResult == nil {
				toolResult = ErrorResult(fmt.Sprintf("tool %s returned no result", tc.Name))
			}

			// Publish tool.result event if bus is available
			if config.MessageBus != nil {
				config.MessageBus.PublishOutbound(bus.OutboundMessage{
					Event:   "tool.result",
					ChatID:  config.ChatID,
					Content: "",
					Metadata: map[string]string{
						"tool": tc.Name,
					},
				})
			}

			// Call verbose callback if provided
			if config.VerboseCallback != nil {
				config.VerboseCallback(iteration, tc.Name, tc.Arguments, toolResult)
			}

			// Determine content for LLM
			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			// Truncate tool results to prevent unbounded memory growth
			// when many tool calls accumulate in the message history.
			const maxToolResultChars = 50000
			if len(contentForLLM) > maxToolResultChars {
				truncated := contentForLLM[:maxToolResultChars]
				contentForLLM = truncated + fmt.Sprintf("\n... (truncated, %d more chars)", len(contentForLLM)-maxToolResultChars)
			}

			// Add tool result message
			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			if config.SessionRecorder != nil && config.SessionKey != "" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, toolResultMsg)
			}
		}

		// 8. Context compaction — if context window is configured and we exceed 75%,
		// summarize old messages to prevent hitting the model's token limit.
		if config.ContextWindow > 0 {
			tokens := EstimateLoopTokens(messages)
			threshold := config.ContextWindow * 75 / 100
			if tokens > threshold {
				logger.InfoCF("toolloop", "Context compaction triggered", map[string]any{
					"tokens":         tokens,
					"threshold":      threshold,
					"context_window": config.ContextWindow,
					"iteration":      iteration,
				})
				// Keep last 6 messages (3 tool call/result pairs) for continuity
				if compacted, ok := CompactLoopMessages(ctx, config.Provider, config.Model, messages, 6); ok {
					messages = compacted
				}
			}
		}
	}

	// Handle case where max iterations was reached without a final response
	if finalContent == "" {
		finalContent = "STATUS: not_done\nSUMMARY: Maximum iterations reached without completing the task\nDETAILS:\nThe subagent ran out of iterations while still using tools. The task may require more steps to complete."
		logger.WarnCF("toolloop", "Max iterations reached without final response",
			map[string]any{
				"iterations": iteration,
				"max":        config.MaxIterations,
			})

		// Only save the synthetic final message when max iterations reached
		// (normal exit with no tool calls already saves inside the loop above)
		if config.SessionRecorder != nil && config.SessionKey != "" {
			config.SessionRecorder.AddFullMessage(config.SessionKey, providers.Message{
				Role:    "assistant",
				Content: finalContent,
			})
		}
	}

	return &ToolLoopResult{
		Content:    finalContent,
		Iterations: iteration,
	}, nil
}
