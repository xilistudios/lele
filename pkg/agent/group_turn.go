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

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// maxGroupToolIterations is the maximum number of tool-call iterations
// within a single group turn before forcing a final response.
const maxGroupToolIterations = 10

// runGroupTurn executes a single group turn: builds the messages (persona+role
// system prompt, shared transcript as context, and the strategy instruction),
// acquires the session semaphore with a key derived as group:<groupID>:<speaker>
// so that different speakers in the same group do not block each other, and
// makes an LLM call. If EnableTools is true, it runs a bounded tool loop:
// the LLM may request tool calls, which are executed and fed back, repeating
// until the LLM produces a final text response or the iteration cap is reached.
// Returns the produced content and tokens used.
func (lr *llmRunnerImpl) runGroupTurn(ctx context.Context, req group.TurnRequest) (string, int, error) {
	// a. Resolve the speaking agent.
	agent, ok := lr.al.registry.GetAgent(req.Speaker)
	if !ok {
		return "", 0, fmt.Errorf("group turn: agent %q not found in registry", req.Speaker)
	}

	// b. Session key for the semaphore — unique per group+speaker.
	sessionKey := "group:" + req.GroupID + ":" + req.Speaker

	// c. Acquire semaphore (same pattern as runAgentLoop).
	sem, _ := lr.al.sessionProcessing.LoadOrStore(sessionKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	select {
	case semCh <- struct{}{}:
		defer func() { <-semCh }()
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	// d. Respect per-turn MaxTokens without mutating the shared AgentInstance.
	if req.MaxTokens > 0 {
		agentCopy := *agent
		agentCopy.MaxTokens = req.MaxTokens
		agent = &agentCopy
	}

	// e. Build initial messages.
	system := req.SystemPrompt
	userContent := req.Instruction
	if req.Transcript != "" {
		userContent = "Panel context (shared transcript):\n" + req.Transcript + "\n\n---\n\n" + req.Instruction
	}
	messages := []providers.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: userContent},
	}

	// f. Build tool definitions if tools are enabled.
	var providerToolDefs []providers.ToolDefinition
	if req.EnableTools {
		providerToolDefs = agent.Tools.ToProviderDefs()

		// Filter out read_image tool if the current model doesn't support vision.
		modelHasVision := getSupportsImages(lr.al.cfg(), agent.Model, extractProviderFromModel(agent.Model, lr.al.cfg().Agents.Defaults.Provider))
		if !modelHasVision {
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, def := range providerToolDefs {
				if def.Function.Name != "read_image" {
					filtered = append(filtered, def)
				}
			}
			providerToolDefs = filtered
		}
	}

	// g. Update tool contexts so tools know which channel/chat they're serving.
	if req.EnableTools && req.OriginChannel != "" {
		lr.al.toolCoordinator.updateToolContexts(agent, req.OriginChannel, req.OriginChatID, sessionKey)
	}

	// h. Bounded tool loop.
	caller := newLLMCaller(lr.al)
	if lr.retryWait != nil {
		caller.retryWait = lr.retryWait
	}
	totalTokens := 0

	for iteration := 1; iteration <= maxGroupToolIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return "", totalTokens, err
		}

		callOpts := llmCallOptions{
			ctx:        ctx,
			agent:      agent,
			messages:   messages,
			toolDefs:   providerToolDefs,
			model:      agent.Model,
			candidates: agent.Candidates,
			sessionKey: sessionKey,
			iteration:  iteration,
			// stream handlers nil — no streaming for group turns
		}

		response, updatedMessages, err := caller.executeWithRetry(callOpts, messages)
		if err != nil {
			return "", totalTokens, fmt.Errorf("group turn LLM call failed: %w", err)
		}
		messages = updatedMessages

		// Track token usage.
		if response != nil && response.Usage != nil {
			totalTokens += response.Usage.TotalTokens
		}

		// No tool calls — we have the final response.
		if response == nil || len(response.ToolCalls) == 0 {
			content := ""
			if response != nil {
				content = response.Content
			}
			return content, totalTokens, nil
		}

		// Tool calls present — execute them.
		// Build assistant message with tool calls.
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

		// Execute each tool call and collect results.
		executor := newToolExecutor(lr.al)
		for _, tc := range response.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", totalTokens, err
			}

			// Serialize tool call arguments for the OnToolCall callback.
			argsJSON := ""
			if tc.Arguments != nil {
				if b, mErr := json.Marshal(tc.Arguments); mErr == nil {
					argsJSON = string(b)
				}
			}

			// Emit "executing" event before running the tool.
			if req.OnToolCall != nil {
				req.OnToolCall(tc.ID, tc.Name, argsJSON, "executing", "")
			}

			toolResult, err := executor.Execute(toolExecOptions{
				ctx:     ctx,
				agent:   agent,
				tc:      tc,
				channel: req.OriginChannel,
				chatID:  req.OriginChatID,
				// Use the group turn session key so tool execution is attributed
				// to the group turn session (group:<groupID>:<speaker>), not the
				// origin chat. This prevents cross-contamination of session state
				// (verbose level, mode checks, approval tracking, subagent routing)
				// between the user's regular chat and the group turn.
				sessionKey: sessionKey,
				iteration:  iteration,
			})

			// Emit "completed" or "error" event after the tool finishes.
			if req.OnToolCall != nil {
				status := "completed"
				resultStr := ""
				if err != nil {
					status = "error"
					resultStr = err.Error()
				} else if toolResult != nil {
					resultStr = buildToolResultContent(toolResult)
				}
				req.OnToolCall(tc.ID, tc.Name, argsJSON, status, resultStr)
			}

			if err != nil {
				return "", totalTokens, fmt.Errorf("group turn tool execution failed: %w", err)
			}

			// Append tool result message.
			contentForLLM := buildToolResultContent(toolResult)
			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Append any context messages from the tool result.
			if toolResult != nil && len(toolResult.ContextMessages) > 0 {
				messages = append(messages, toolResult.ContextMessages...)
			}
		}

		logger.DebugCF("agent", "Group turn tool loop iteration completed",
			map[string]interface{}{
				"speaker":    req.Speaker,
				"group_id":   req.GroupID,
				"iteration":  iteration,
				"tool_calls": len(response.ToolCalls),
			})
	}

	// If we exhausted all iterations, make one final call without tools
	// to force a text response.
	logger.WarnCF("agent", "Group turn tool loop exhausted, forcing final response",
		map[string]interface{}{
			"speaker":  req.Speaker,
			"group_id": req.GroupID,
		})

	callOpts := llmCallOptions{
		ctx:        ctx,
		agent:      agent,
		messages:   messages,
		toolDefs:   nil, // no tools — force a text response
		model:      agent.Model,
		candidates: agent.Candidates,
		sessionKey: sessionKey,
		iteration:  maxGroupToolIterations + 1,
	}
	response, _, err := caller.executeWithRetry(callOpts, messages)
	if err != nil {
		return "", totalTokens, fmt.Errorf("group turn final LLM call failed: %w", err)
	}
	if response != nil && response.Usage != nil {
		totalTokens += response.Usage.TotalTokens
	}

	content := ""
	if response != nil {
		content = response.Content
	}
	return content, totalTokens, nil
}
