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
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/bus"
	leleconfig "github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
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

// SessionCompactor applies a loop-compaction result to the persisted session
// of a subagent. It is implemented by *session.Manager (CompactSession) and
// keeps the persisted summary/exclusion state in sync with the in-memory
// compacted message list, so compaction survives restarts and the persisted
// session does not grow unbounded. Optional: when nil, compaction still runs
// in memory but is not persisted.
type SessionCompactor interface {
	CompactSession(key string, summary string, keepCount int, evict bool) error
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
	Retry           *RetryConfig // Retry config for LLM calls. nil = no retry.
	ContextWindow   int          // Max tokens for context. 0 = no compaction.
	// CompactionThresholdPercent is the percentage of ContextWindow at which
	// intra-loop compaction triggers. 0 (or out of range) = use the default
	// (pkg/config.DefaultCompactionThresholdPercent).
	CompactionThresholdPercent int
	MessageBus                 *bus.MessageBus   // Optional: publish real-time events to TUI.
	Channel                    string            // Origin channel for events.
	ChatID                     string            // Origin chatID for events (subagent sessionKey).
	VisionSupported            bool              // Whether the model supports vision. When false, read_image is filtered from tool defs.
	Redactor                   *keyring.Redactor // Optional: redacts secret values from tool results before they enter context.
	// RetryWait optionally overrides the wait function used between
	// empty-response retries (nil means time.After). Used by tests to
	// avoid real sleeps.
	RetryWait func(time.Duration) <-chan time.Time
	// CompactionModel optionally overrides the model used for summarization
	// during context compaction (proactive and reactive). Empty = use Model.
	CompactionModel string
	// SessionCompactor optionally syncs compaction results to the persisted
	// session (implemented by *session.Manager). nil = in-memory only.
	SessionCompactor SessionCompactor
	// EvictExcludedFromMemory controls whether session-synced compaction also
	// evicts excluded messages from the in-memory session cache.
	EvictExcludedFromMemory bool
	// OwnerAgentID and OwnerSessionKey identify the agent loop that owns this
	// tool loop. When OwnerSessionKey is set, RunToolLoop injects them into
	// the tool-execution context (tools.WithAgentToolContext) so tools run
	// inside the loop can attribute their work - spawned subagents, background
	// processes - to the owning session for cancellation (issue #230). The
	// main agent loop gets this via the agent tool executor; standalone tool
	// loops (subagents, sync subagent tool) must set it explicitly.
	OwnerAgentID    string
	OwnerSessionKey string
}

// syncCompactionToSession persists a loop-compaction result to the subagent's
// session via the optional SessionCompactor. The summary is the second
// message of the compacted list ("[Context compacted — summary of ...]");
// keepCount=6 matches the loop's keepLast so the persisted exclusion window
// mirrors the in-memory one. Failures are logged and swallowed: compaction
// is an optimization, and a persistence error must not kill a running task.
func syncCompactionToSession(config ToolLoopConfig, summary string) {
	if config.SessionCompactor == nil || config.SessionKey == "" || summary == "" {
		return
	}
	if err := config.SessionCompactor.CompactSession(config.SessionKey, summary, 6, config.EvictExcludedFromMemory); err != nil {
		logger.WarnCF("toolloop", "Failed to sync compaction to session", map[string]any{
			"session_key": config.SessionKey,
			"error":       err.Error(),
		})
	}
}

// maxReactiveCompactions is the maximum number of reactive compaction attempts
// (context-overflow recovery) allowed between successful LLM responses. It
// prevents infinite compact-fail loops when even the compacted context exceeds
// the model's window.
const maxReactiveCompactions = 3

// toolLoopEmptyRetryBackoff returns the wait duration before the given
// empty-response retry attempt (1s, 2s, 3s, capped at 3s). Empty responses
// are retried indefinitely — bounded by MaxIterations (when set) and by
// context cancellation — with the backoff capped at 3s to avoid a tight
// spin when a provider repeatedly returns HTTP 200 with empty content.
func toolLoopEmptyRetryBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
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

	// Already compacted with nothing new added since (messages[1] is our
	// summary message and the context is exactly the compacted shape
	// [system, summary, continue, tail×keepLast]): re-summarizing would
	// waste an LLM call and cannot shrink below the compacted size.
	if len(messages) <= keepLast+3 &&
		strings.HasPrefix(messages[1].Content, "[Context compacted") {
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
	resp, err := providers.ChatIdle(ctx, provider, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, map[string]interface{}{
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

	// Trim the tail to a safe boundary. The kept tail must not start with a
	// tool message whose assistant tool_calls message was summarized away,
	// nor with an assistant tool_calls message whose tool results were cut
	// off — providers reject both sequences. See safeTailBoundary for the
	// boundary rules.
	if bi := safeTailBoundary(tail); bi > 0 {
		tail = tail[bi:]
	} else if bi < 0 {
		// No safe boundary exists in the tail (e.g. it is all orphaned tool
		// results). Compacting would produce an invalid message sequence, so
		// skip compaction entirely for this round.
		logger.WarnCF("toolloop", "Compaction skipped: no safe tail boundary", map[string]any{
			"tail_len": len(tail),
		})
		return messages, false
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

// safeTailBoundary returns the index of the first safe boundary in the given
// tail slice, or -1 if none exists. A boundary at index i means the slice may
// start at i without producing an invalid assistant/tool sequence:
//
//   - The message at i must not be a tool result (role "tool"): its assistant
//     tool_calls message would precede the boundary and be summarized away.
//   - The remaining slice tail[i:] must be self-consistent: every tool result
//     must be preceded (within the remaining slice) by the assistant message
//     carrying the matching tool_calls ID, and every assistant tool_calls
//     message must have all of its results present in the remaining slice.
//
// The first (smallest) safe boundary is returned so the tail keeps as much
// recent context as possible. Tails are short (keepLast is typically 6), so
// the O(n²) self-consistency rescan is negligible.
func safeTailBoundary(tail []providers.Message) int {
	// isSelfConsistent reports whether every tool result in msgs is preceded
	// by its assistant tool_calls message and every tool call is answered.
	isSelfConsistent := func(msgs []providers.Message) bool {
		pending := map[string]bool{}
		for _, m := range msgs {
			switch m.Role {
			case "assistant":
				for _, tc := range m.ToolCalls {
					if tc.ID != "" {
						pending[tc.ID] = true
					}
				}
			case "tool":
				if !pending[m.ToolCallID] {
					return false // orphaned result: assistant was trimmed
				}
				delete(pending, m.ToolCallID)
			}
		}
		return len(pending) == 0 // false when calls lack their results
	}

	for i := 0; i < len(tail); i++ {
		if tail[i].Role == "tool" {
			continue
		}
		if isSelfConsistent(tail[i:]) {
			return i
		}
	}
	return -1
}

// RunToolLoop executes the LLM + tool call iteration loop.
// This is the core agent logic that can be reused by both main agent and subagents.
func RunToolLoop(ctx context.Context, config ToolLoopConfig, messages []providers.Message, channel, chatID string) (*ToolLoopResult, error) {
	iteration := 0
	emptyRetries := 0
	reactiveCompactions := 0
	var finalContent string

	// Attribute tool executions inside this loop to the owning session so
	// nested spawns/background processes can be cancelled with it (#230).
	// When the config does not name an owner explicitly, inherit the agent
	// tool context already carried by ctx (a synchronous subagent tool runs
	// inside its caller's context, so the caller owns whatever it spawns).
	if config.OwnerSessionKey == "" {
		config.OwnerAgentID, config.OwnerSessionKey = AgentToolContextFromCtx(ctx)
	}
	if config.OwnerSessionKey != "" {
		ctx = WithAgentToolContext(ctx, config.OwnerAgentID, config.OwnerSessionKey)
	}

	if config.SessionRecorder != nil && config.SessionKey != "" {
		for _, m := range messages {
			if m.Role == "user" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, m)
				break
			}
		}
	}

	// MaxIterations <= 0 means unlimited (timeout is the real safety guard)
	for config.MaxIterations <= 0 || iteration < config.MaxIterations {
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

		// Filter out read_image when the model doesn't support vision.
		// Mirrors the filtering in the main agent loop (pkg/agent/llm_runner.go).
		if !config.VisionSupported {
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, def := range providerToolDefs {
				if def.Function.Name != "read_image" {
					filtered = append(filtered, def)
				}
			}
			providerToolDefs = filtered
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
		for {
			if config.Retry != nil {
				response, err = ChatWithRetry(ctx, config.Provider, messages, providerToolDefs, apiModel, llmOpts, *config.Retry)
			} else {
				// Streaming transport: bounded by an idle timeout so a
				// long-reasoning subagent is not killed at 120s of wall clock.
				response, err = providers.ChatIdle(ctx, config.Provider, messages, providerToolDefs, apiModel, llmOpts)
			}
			if err == nil {
				// Success resets the reactive compaction budget so a later
				// overflow in the same run gets a fresh set of attempts.
				reactiveCompactions = 0
				break
			}
			// Reactive compaction: when the API rejects the prompt because it
			// exceeds the model's context window, summarize old messages and
			// retry instead of failing the whole subagent task. Mirrors the
			// main agent loop's executeWithRetry behavior (pkg/agent/llm_caller.go).
			// The budget (3 attempts per successful response) prevents infinite
			// compact-fail loops when even the compacted context is too large.
			if config.ContextWindow > 0 &&
				reactiveCompactions < maxReactiveCompactions &&
				providers.IsContextOverflowError(err) {
				reactiveCompactions++
				logger.WarnCF("toolloop", "Context overflow detected, attempting reactive compaction",
					map[string]any{
						"iteration":    iteration,
						"attempt":      reactiveCompactions,
						"max_attempts": maxReactiveCompactions,
						"error":        err.Error(),
					})
				compactModel := config.Model
				if config.CompactionModel != "" {
					compactModel = config.CompactionModel
				}
				if compacted, ok := CompactLoopMessages(ctx, config.Provider, compactModel, messages, 6); ok {
					messages = compacted
					if config.MessageBus != nil {
						config.MessageBus.PublishOutbound(bus.OutboundMessage{
							Event:   "tool.result",
							ChatID:  config.ChatID,
							Content: "",
							Metadata: map[string]string{
								"tool":   "compact",
								"result": "Context overflow — compacted and retrying",
							},
						})
					}
					syncCompactionToSession(config, compacted[1].Content)
					continue
				}
				// Compaction could not reduce the context (e.g. nothing left
				// to summarize or the summarizer failed) — fall through to
				// returning the error.
			}
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

			// If the response is empty, retry by prompting the model again. This
			// keeps the subagent loop alive when a provider returns HTTP 200 with
			// empty content instead of terminating the run with a misleading
			// "maximum iterations reached" fallback. The retry loop is bounded by
			// MaxIterations (when set) and by context cancellation; with unlimited
			// iterations it keeps retrying until a non-empty response arrives.
			if len(strings.TrimSpace(finalContent)) == 0 {
				emptyRetries++
				logger.WarnCF("toolloop", "Empty response received, retrying with follow-up prompt",
					map[string]any{
						"iteration": iteration,
						"retry":     emptyRetries,
					})
				wait := toolLoopEmptyRetryBackoff(emptyRetries - 1)
				waitFn := config.RetryWait
				if waitFn == nil {
					waitFn = time.After
				}
				select {
				case <-waitFn(wait):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				messages = append(messages, providers.Message{
					Role:    "user",
					Content: "Your previous response was empty. Please continue working on the task and provide your final response.",
				})
				continue
			}
			// Non-empty final content: reset the consecutive-empty counter so the
			// backoff restarts small after a real response.
			emptyRetries = 0

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

			// Redact known secret values before they enter the LLM context.
			if config.Redactor != nil {
				contentForLLM = config.Redactor.Redact(contentForLLM)
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

		// 8. Context compaction — if context window is configured and we exceed
		// the configured threshold percent, summarize old messages to prevent
		// hitting the model's token limit.
		if config.ContextWindow > 0 {
			tokens := EstimateLoopTokens(messages)
			percent := config.CompactionThresholdPercent
			if percent <= 0 || percent > 100 {
				percent = leleconfig.DefaultCompactionThresholdPercent
			}
			threshold := config.ContextWindow * percent / 100
			if tokens > threshold {
				logger.InfoCF("toolloop", "Context compaction triggered", map[string]any{
					"tokens":            tokens,
					"threshold":         threshold,
					"context_window":    config.ContextWindow,
					"threshold_percent": percent,
					"iteration":         iteration,
				})
				// Keep last 6 messages (3 tool call/result pairs) for continuity
				compactModel := config.Model
				if config.CompactionModel != "" {
					compactModel = config.CompactionModel
				}
				if compacted, ok := CompactLoopMessages(ctx, config.Provider, compactModel, messages, 6); ok {
					messages = compacted
					if config.MessageBus != nil {
						config.MessageBus.PublishOutbound(bus.OutboundMessage{
							Event:   "tool.result",
							ChatID:  config.ChatID,
							Content: "",
							Metadata: map[string]string{
								"tool":   "compact",
								"result": fmt.Sprintf("Tokens: ~%d → ~%d", tokens, EstimateLoopTokens(compacted)),
							},
						})
					}
					syncCompactionToSession(config, compacted[1].Content)
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
