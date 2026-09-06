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

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// maxGroupToolIterations is the maximum number of tool-call iterations
// within a single group turn before forcing a final response.
const maxGroupToolIterations = 10

// groupTurnExcludedTools are never offered to group participants:
// group_chat would allow unbounded nested group trees (B8).
//
// A participant whose toolset still advertises group_chat can invoke it from
// inside a group turn and spawn sub-groups, recursively, with no depth limit —
// an unbounded tree of panels burning tokens. The tool is therefore stripped
// from the definitions sent to the model (the same guard spawn/subagents have
// via CloneWithout in tool_coordinator.go).
var groupTurnExcludedTools = map[string]bool{"group_chat": true}

// filterToolDefs returns the tool definitions a model should actually be
// offered, dropping anything it cannot serve:
//
//   - when hasVision is false, read_image is removed (the model could not
//     interpret the returned image content);
//   - every name present as true in excluded is removed regardless of vision.
//
// It is a pure function so the policy can be unit-tested without building a
// full runner. A nil excluded map simply means "only apply the vision filter".
func filterToolDefs(defs []providers.ToolDefinition, hasVision bool, excluded map[string]bool) []providers.ToolDefinition {
	filtered := make([]providers.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		name := def.Function.Name
		if !hasVision && name == "read_image" {
			continue
		}
		if excluded[name] {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

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
		// Release the token, then drop the map entry if nobody else holds or
		// waits on it (B6). Without the Delete, every (group, speaker) pair
		// ever seen left a channel in the sync.Map for the lifetime of the
		// process. Concurrent turns for the same key do not exist by design
		// (one speaker per turn; Parallel means distinct speakers, hence
		// distinct keys), and the len check keeps the cleanup safe even if a
		// second holder queues up: it only deletes an idle channel, while a
		// racing LoadOrStore simply creates a fresh one.
		defer func() {
			<-semCh
			if len(semCh) == 0 {
				lr.al.sessionProcessing.Delete(sessionKey)
			}
		}()
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
	// Vision is determined by the primary model only. When the fallback chain
	// fails over to a non-vision model, image content is stripped per-candidate
	// in callWithFallback (see llm_caller.go).
	var providerToolDefs []providers.ToolDefinition
	modelHasVision := getSupportsImages(lr.al.cfg(), agent.Model, extractProviderFromModel(agent.Model, lr.al.cfg().Agents.Defaults.Provider))
	if req.EnableTools {
		providerToolDefs = agent.Tools.ToProviderDefs()

		// Drop tools the model must not be offered during a group turn:
		// read_image when the primary model has no vision (it could not
		// understand the returned image content) and group_chat, which would
		// let a participant spawn nested group trees (B8). Only the definitions
		// handed to the provider are filtered — a model cannot call what it
		// does not see, so no second guard is needed in the executor.
		providerToolDefs = filterToolDefs(providerToolDefs, modelHasVision, groupTurnExcludedTools)
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
		assistantMsg.ToolCalls = providers.CanonicalToolCalls(response.ToolCalls)

		// Every call was rejected by canonicalisation. Running them anyway
		// would append tool results whose ids match nothing in the assistant
		// message above, and the next iteration would be sent a request the
		// provider rejects outright. Ask for a retry instead.
		if len(assistantMsg.ToolCalls) == 0 {
			logger.WarnCF("agent", "group turn returned only invalid tool calls",
				map[string]interface{}{
					"speaker":     req.Speaker,
					"group_id":    req.GroupID,
					"iteration":   iteration,
					"rejected":    len(response.ToolCalls),
					"session_key": sessionKey,
				})
			// Keep the assistant turn in the live context so the guidance below
			// stays a valid user message: providers reject two consecutive
			// messages of the same role. A blank assistant is kept in memory
			// only, mirroring the empty-response handling in the main loop.
			if strings.TrimSpace(response.Content) != "" {
				messages = append(messages, assistantMsg)
			} else {
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
				})
			}
			messages = append(messages, providers.Message{
				Role: "user",
				Content: "Your previous tool call was malformed and could not be parsed " +
					"(a tool name and a valid JSON object of arguments are required). " +
					"Please retry using a proper function call.",
			})
			continue
		}

		messages = append(messages, assistantMsg)

		// Execute each tool call and collect results. Iterating the canonical
		// list keeps every tool result aligned with a persisted tool_call id.
		executor := newToolExecutor(lr.al)
		for _, tc := range assistantMsg.ToolCalls {
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
					resultStr = lr.al.redactor.Redact(buildToolResultContent(toolResult))
				}
				req.OnToolCall(tc.ID, tc.Name, argsJSON, status, resultStr)
			}

			if err != nil {
				return "", totalTokens, fmt.Errorf("group turn tool execution failed: %w", err)
			}

			// Append tool result message.
			contentForLLM := lr.al.redactor.Redact(buildToolResultContent(toolResult))
			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			// Append any context messages from the tool result.
			if toolResult != nil && len(toolResult.ContextMessages) > 0 {
				ctxMsgs := toolResult.ContextMessages
				if !modelHasVision {
					ctxMsgs = stripImageContentParts(ctxMsgs)
				}
				messages = append(messages, ctxMsgs...)
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
