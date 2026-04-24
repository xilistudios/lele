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

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/utils"
)

// VerboseCallback is called when verbose mode is enabled to notify about tool execution progress.
type VerboseCallback func(iteration int, toolName string, args map[string]interface{}, result *ToolResult)

type SessionRecorder interface {
	AddFullMessage(sessionKey string, msg providers.Message)
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
}

// ToolLoopResult contains the result of running the tool loop.
type ToolLoopResult struct {
	Content    string
	Iterations int
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
		response, err := config.Provider.Chat(ctx, messages, providerToolDefs, config.Model, llmOpts)
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
			messages = append(messages, assistantMsg)
			if config.SessionRecorder != nil && config.SessionKey != "" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, assistantMsg)
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
			if config.Tools != nil {
				toolResult = config.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, channel, chatID, nil)
			} else {
				toolResult = ErrorResult("No tools available")
			}

			if toolResult == nil {
				toolResult = ErrorResult(fmt.Sprintf("tool %s returned no result", tc.Name))
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
