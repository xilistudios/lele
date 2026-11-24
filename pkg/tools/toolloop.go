package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	leleconfig "github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/utils"
)

type VerboseCallback func(iteration int, toolName string, args map[string]interface{}, result *ToolResult)

type SessionRecorder interface {
	AddFullMessage(sessionKey string, msg providers.Message)
	Save(sessionKey string) error
}

func recordMessage(config ToolLoopConfig, msg providers.Message) {
	if config.SessionRecorder != nil && config.SessionKey != "" {
		config.SessionRecorder.AddFullMessage(config.SessionKey, msg)
	}
}

type SessionCompactor interface {
	CompactSession(key string, summary string, keepCount int, evict bool) error
}

type ToolLoopConfig struct {
	Provider                   providers.LLMProvider
	Model                      string
	Tools                      *ToolRegistry
	MaxIterations              int
	LLMOptions                 map[string]any
	VerboseCallback            VerboseCallback
	SessionRecorder            SessionRecorder
	SessionKey                 string
	Retry                      *RetryConfig
	ContextWindow              int
	CompactionThresholdPercent int
	MessageBus                 *bus.MessageBus
	Channel                    string
	ChatID                     string
	VisionSupported            bool
	VideoSupported             bool
	Redactor                   *keyring.Redactor
	RetryWait                  func(time.Duration) <-chan time.Time
	CompactionModel            string
	SessionCompactor           SessionCompactor
	EvictExcludedFromMemory    bool
	OwnerAgentID               string
	OwnerSessionKey            string
	OnTokenUsage               func(inputTokens, outputTokens int)
}

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

const maxReactiveCompactions = 3

const maxConsecutiveEmptyResponses = 5

const toolLoopMaxIterationsContent = "STATUS: not_done\nSUMMARY: Maximum iterations reached without completing the task\nDETAILS:\nThe subagent ran out of iterations while still using tools. The task may require more steps to complete."

const toolLoopEmptyResponseContent = "STATUS: not_done\nSUMMARY: The model returned empty responses repeatedly\nDETAILS:\nThe provider kept returning blank content (likely a thinking model exhausting its token budget on reasoning). The run was stopped to avoid an infinite retry loop."

func toolLoopEmptyRetryBackoff(attempt int) time.Duration {
	backoffs := []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second}
	if attempt >= len(backoffs) {
		return backoffs[len(backoffs)-1]
	}
	return backoffs[attempt]
}

type ToolLoopResult struct {
	Content    string
	Iterations int
}

func EstimateLoopTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += providers.MessageCharCost(m)
	}
	return totalChars * 2 / 5
}

func CompactLoopMessages(ctx context.Context, provider providers.LLMProvider, model string, messages []providers.Message, keepLast int, sessionKey string) ([]providers.Message, bool) {
	if len(messages) <= keepLast+2 {
		return messages, false
	}

	if len(messages) <= keepLast+3 &&
		strings.HasPrefix(messages[1].Content, "[Context compacted") {
		return messages, false
	}

	systemMsg := messages[0]
	tail := messages[len(messages)-keepLast:]
	middle := messages[1 : len(messages)-keepLast]

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
	compactOpts := providers.WithWireSessionID(map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	}, sessionKey)
	resp, err := providers.ChatIdle(ctx, provider, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, compactOpts)

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

	continueMsg := providers.Message{
		Role:    "user",
		Content: "[The context was compacted to save space. You were in the middle of a multi-step task. Review the summary above and the recent tool results below, then CONTINUE executing the task using the available tools. Do not stop or ask for confirmation — resume where you left off.]",
	}

	if bi := safeTailBoundary(tail); bi > 0 {
		tail = tail[bi:]
	} else if bi < 0 {
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

func safeTailBoundary(tail []providers.Message) int {
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
					return false
				}
				delete(pending, m.ToolCallID)
			}
		}
		return len(pending) == 0
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

func RunToolLoop(ctx context.Context, config ToolLoopConfig, messages []providers.Message, channel, chatID string) (*ToolLoopResult, error) {
	iteration := 0
	emptyRetries := 0
	reactiveCompactions := 0
	var finalContent string

	if config.OwnerAgentID == "" && config.OwnerSessionKey == "" {
		config.OwnerAgentID, config.OwnerSessionKey = AgentToolContextFromCtx(ctx)
	}
	if config.OwnerAgentID != "" || config.OwnerSessionKey != "" {
		ctx = WithAgentToolContext(ctx, config.OwnerAgentID, config.OwnerSessionKey)
	}

	wireSessionKey := config.SessionKey
	if wireSessionKey == "" {
		wireSessionKey = config.OwnerSessionKey
	}

	if config.SessionRecorder != nil && config.SessionKey != "" {
		for _, m := range messages {
			if m.Role == "user" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, m)
				break
			}
		}
	}

	for config.MaxIterations <= 0 || iteration < config.MaxIterations {
		iteration++

		logger.DebugCF("toolloop", "LLM iteration",
			map[string]any{
				"iteration": iteration,
				"max":       config.MaxIterations,
			})

		var providerToolDefs []providers.ToolDefinition
		if config.Tools != nil {
			providerToolDefs = config.Tools.ToProviderDefs()
		}

		if !config.VisionSupported || !config.VideoSupported {
			filtered := make([]providers.ToolDefinition, 0, len(providerToolDefs))
			for _, def := range providerToolDefs {
				if !config.VisionSupported && def.Function.Name == "read_image" {
					continue
				}
				if !config.VideoSupported && !config.VisionSupported && def.Function.Name == "read_video" {
					continue
				}
				filtered = append(filtered, def)
			}
			providerToolDefs = filtered
		}

		llmOpts := config.LLMOptions
		if llmOpts == nil {
			llmOpts = map[string]any{}
		}
		llmOpts = providers.WithWireSessionID(llmOpts, wireSessionKey)
		apiModel := providers.StripProviderPrefix(config.Model)

		var response *providers.LLMResponse
		var err error
		for {
			if healed, changed := providers.HealToolCallPairs(messages); changed {
				messages = healed
			}

			if config.Retry != nil {
				response, err = ChatWithRetry(ctx, config.Provider, messages, providerToolDefs, apiModel, llmOpts, *config.Retry)
			} else {
				response, err = providers.ChatIdle(ctx, config.Provider, messages, providerToolDefs, apiModel, llmOpts)
			}
			if err == nil {
				reactiveCompactions = 0
				break
			}
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
				if compacted, ok := CompactLoopMessages(ctx, config.Provider, compactModel, messages, 6, wireSessionKey); ok {
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
			}
			logger.ErrorCF("toolloop", "LLM call failed",
				map[string]any{
					"iteration": iteration,
					"error":     err.Error(),
				})
			return nil, fmt.Errorf("LLM call failed: %w", err)
		}

		if config.OnTokenUsage != nil {
			in, out := providers.ResponseTokenCounts(messages, response)
			config.OnTokenUsage(in, out)
		}

		if len(response.ToolCalls) == 0 {
			finalContent = response.Content
			logger.InfoCF("toolloop", "LLM response without tool calls (direct answer)",
				map[string]any{
					"iteration":     iteration,
					"content_chars": len(finalContent),
				})

			if len(strings.TrimSpace(finalContent)) == 0 {
				emptyRetries++
				if emptyRetries > maxConsecutiveEmptyResponses {
					logger.ErrorCF("toolloop", "Empty response limit reached, ending run",
						map[string]any{
							"iteration": iteration,
							"retries":   emptyRetries,
							"max":       maxConsecutiveEmptyResponses,
						})
					finalContent = toolLoopEmptyResponseContent
					if config.SessionRecorder != nil && config.SessionKey != "" {
						config.SessionRecorder.AddFullMessage(config.SessionKey, providers.Message{
							Role:    "assistant",
							Content: finalContent,
						})
					}
					break
				}
				logger.WarnCF("toolloop", "Empty response received, retrying with follow-up prompt",
					map[string]any{
						"iteration": iteration,
						"retry":     emptyRetries,
					})
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
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
			emptyRetries = 0

			assistantMsg := providers.Message{
				Role:             "assistant",
				Content:          response.Content,
				ReasoningContent: response.ReasoningContent,
			}
			if config.SessionRecorder != nil && config.SessionKey != "" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, assistantMsg)
			}

			if config.MessageBus != nil && finalContent != "" {
				config.MessageBus.PublishOutbound(bus.OutboundMessage{
					Event:   "message.stream",
					ChatID:  config.ChatID,
					Content: finalContent,
				})
			}
			break
		}
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

		assistantMsg := providers.Message{
			Role:             "assistant",
			Content:          response.Content,
			ReasoningContent: response.ReasoningContent,
		}
		assistantMsg.ToolCalls = providers.CanonicalToolCalls(response.ToolCalls)

		if len(assistantMsg.ToolCalls) == 0 {
			logger.WarnCF("toolloop", "all tool calls from response were invalid, requesting a retry", map[string]any{
				"iteration": iteration,
				"rejected":  len(response.ToolCalls),
			})
			if strings.TrimSpace(response.Content) != "" {
				messages = append(messages, assistantMsg)
				recordMessage(config, assistantMsg)
			} else {
				messages = append(messages, providers.Message{
					Role:             "assistant",
					Content:          response.Content,
					ReasoningContent: response.ReasoningContent,
				})
			}
			guidanceMsg := providers.Message{
				Role: "user",
				Content: "\u26a0\ufe0f Your previous tool call was malformed and could not be parsed " +
					"(a tool name and a valid JSON object of arguments are required). " +
					"Please retry using a proper function call.",
			}
			messages = append(messages, guidanceMsg)
			recordMessage(config, guidanceMsg)
			continue
		}

		messages = append(messages, assistantMsg)
		recordMessage(config, assistantMsg)

		// Multimodal context messages (e.g. the image loaded by read_image)
		// are collected here and injected after ALL tool results, mirroring
		// the main agent loop (pkg/agent/llm_runner.go). Without this the
		// subagent only sees the tool's textual ForLLM summary and never the
		// image itself.
		var contextMsgs []providers.Message

		for _, tc := range assistantMsg.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Arguments)
			argsPreview := utils.Truncate(string(argsJSON), 200)
			logger.InfoCF("toolloop", fmt.Sprintf("Tool call: %s(%s)", tc.Name, argsPreview),
				map[string]any{
					"tool":      tc.Name,
					"iteration": iteration,
				})

			var toolResult *ToolResult

			if config.MessageBus != nil {
				action := tc.Name
				if argsPreview != "" {
					action = fmt.Sprintf("%s(%s)", tc.Name, argsPreview)
				}
				metadata := map[string]string{
					"tool":      tc.Name,
					"action":    action,
					"arguments": string(argsJSON),
				}
				if tc.ArgumentsTruncated {
					metadata["truncated"] = "true"
				}
				config.MessageBus.PublishOutbound(bus.OutboundMessage{
					Event:    "tool.executing",
					ChatID:   config.ChatID,
					Content:  "",
					Metadata: metadata,
				})
			}

			if tc.ArgumentsTruncated {
				toolResult = TruncatedArgumentsError(tc)
			} else if config.Tools != nil {
				toolResult = config.Tools.ExecuteWithContext(ctx, tc.Name, tc.Arguments, channel, chatID, nil)
			} else {
				toolResult = ErrorResult("No tools available")
			}

			if toolResult == nil {
				toolResult = ErrorResult(fmt.Sprintf("tool %s returned no result", tc.Name))
			}

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

			if config.VerboseCallback != nil {
				config.VerboseCallback(iteration, tc.Name, tc.Arguments, toolResult)
			}

			contentForLLM := toolResult.ForLLM
			if contentForLLM == "" && toolResult.Err != nil {
				contentForLLM = toolResult.Err.Error()
			}

			if config.Redactor != nil {
				contentForLLM = config.Redactor.Redact(contentForLLM)
			}

			const maxToolResultChars = 50000
			if len(contentForLLM) > maxToolResultChars {
				truncated := contentForLLM[:maxToolResultChars]
				contentForLLM = truncated + fmt.Sprintf("\n... (truncated, %d more chars)", len(contentForLLM)-maxToolResultChars)
			}

			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			if config.SessionRecorder != nil && config.SessionKey != "" {
				config.SessionRecorder.AddFullMessage(config.SessionKey, toolResultMsg)
			}

			contextMsgs = append(contextMsgs, toolResult.ContextMessages...)
		}

		// Inject context messages (role "user") after all tool messages so the
		// tool results stay contiguous right after the assistant tool_calls
		// message, as providers require. Strip modalities the model lacks.
		if len(contextMsgs) > 0 {
			if !config.VisionSupported || !config.VideoSupported {
				contextMsgs = providers.StripContentParts(contextMsgs, !config.VisionSupported, !config.VideoSupported)
			}
			for _, ctxMsg := range contextMsgs {
				messages = append(messages, ctxMsg)
				recordMessage(config, ctxMsg)
			}
		}

		if reduced, evicted := EnforceImageBudget(messages); evicted > 0 {
			messages = reduced
			logger.InfoCF("toolloop", "Request image budget enforced", map[string]any{
				"evicted_parts": evicted,
				"request_bytes": providers.EstimateRequestBytes(messages),
				"budget_bytes":  leleconfig.DefaultMaxRequestBytes,
			})
		}
		if config.ContextWindow > 0 || RequestOverBudget(messages) {
			tokens := EstimateLoopTokens(messages)
			percent := config.CompactionThresholdPercent
			if percent <= 0 || percent > 100 {
				percent = leleconfig.DefaultCompactionThresholdPercent
			}
			threshold := config.ContextWindow * percent / 100
			if tokens > threshold || RequestOverBudget(messages) {
				logger.InfoCF("toolloop", "Context compaction triggered", map[string]any{
					"tokens":            tokens,
					"threshold":         threshold,
					"context_window":    config.ContextWindow,
					"threshold_percent": percent,
					"iteration":         iteration,
				})
				compactModel := config.Model
				if config.CompactionModel != "" {
					compactModel = config.CompactionModel
				}
				if compacted, ok := CompactLoopMessages(ctx, config.Provider, compactModel, messages, 6, wireSessionKey); ok {
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

	if finalContent == "" {
		finalContent = toolLoopMaxIterationsContent
		logger.WarnCF("toolloop", "Max iterations reached without final response",
			map[string]any{
				"iterations": iteration,
				"max":        config.MaxIterations,
			})

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
