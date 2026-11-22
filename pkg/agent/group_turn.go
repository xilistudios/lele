package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

const maxGroupToolIterations = 10

var groupTurnExcludedTools = map[string]bool{"group_chat": true}

func filterToolDefs(defs []providers.ToolDefinition, hasVision, hasVideo bool, excluded map[string]bool) []providers.ToolDefinition {
	filtered := make([]providers.ToolDefinition, 0, len(defs))
	for _, def := range defs {
		name := def.Function.Name
		if !hasVision && name == "read_image" {
			continue
		}
		if !hasVideo && !hasVision && name == "read_video" {
			continue
		}
		if excluded[name] {
			continue
		}
		filtered = append(filtered, def)
	}
	return filtered
}

func (lr *llmRunnerImpl) runGroupTurn(ctx context.Context, req group.TurnRequest) (string, int, error) {
	agent, ok := lr.al.registry.GetAgent(req.Speaker)
	if !ok {
		return "", 0, fmt.Errorf("group turn: agent %q not found in registry", req.Speaker)
	}

	sessionKey := "group:" + req.GroupID + ":" + req.Speaker

	sem, _ := lr.al.sessionProcessing.LoadOrStore(sessionKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	select {
	case semCh <- struct{}{}:
		defer func() {
			<-semCh
			if len(semCh) == 0 {
				lr.al.sessionProcessing.Delete(sessionKey)
			}
		}()
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}

	if req.MaxTokens > 0 {
		agentCopy := *agent
		agentCopy.MaxTokens = req.MaxTokens
		agent = &agentCopy
	}

	system := req.SystemPrompt
	userContent := req.Instruction
	if req.Transcript != "" {
		userContent = "Panel context (shared transcript):\n" + req.Transcript + "\n\n---\n\n" + req.Instruction
	}
	messages := []providers.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: userContent},
	}

	var providerToolDefs []providers.ToolDefinition
	modelHasVision := getSupportsImages(lr.al.cfg(), agent.Model, extractProviderFromModel(agent.Model, lr.al.cfg().Agents.Defaults.Provider))
	modelHasVideo := getSupportsVideo(lr.al.cfg(), agent.Model, extractProviderFromModel(agent.Model, lr.al.cfg().Agents.Defaults.Provider))
	registry := lr.al.toolCoordinator.sessionTools(agent, sessionKey)
	if registry == nil {
		registry = agent.Tools
	}
	if req.EnableTools {
		providerToolDefs = registry.ToProviderDefs()

		providerToolDefs = filterToolDefs(providerToolDefs, modelHasVision, modelHasVideo, groupTurnExcludedTools)
	}

	if req.EnableTools && req.OriginChannel != "" {
		lr.al.toolCoordinator.updateToolContexts(agent, req.OriginChannel, req.OriginChatID, sessionKey)
	}

	caller := newLLMCaller(lr.al)
	if lr.retryWait != nil {
		caller.retryWait = lr.retryWait
	}
	totalTokens := 0

	for iteration := 1; iteration <= maxGroupToolIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			return "", totalTokens, err
		}

		if reduced, evicted := tools.EnforceImageBudget(messages); evicted > 0 {
			messages = reduced
			logger.InfoCF("agent", "Group turn request image budget enforced", map[string]interface{}{
				"group_id": req.GroupID, "speaker": req.Speaker, "iteration": iteration,
				"evicted_parts": evicted,
			})
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
		}

		response, updatedMessages, err := caller.executeWithRetry(callOpts, messages)
		if err != nil {
			return "", totalTokens, fmt.Errorf("group turn LLM call failed: %w", err)
		}
		messages = updatedMessages

		if response != nil && response.Usage != nil {
			totalTokens += response.Usage.TotalTokens
		}

		if response == nil || len(response.ToolCalls) == 0 {
			content := ""
			if response != nil {
				content = response.Content
			}
			return content, totalTokens, nil
		}

		assistantMsg := providers.Message{
			Role:             "assistant",
			Content:          response.Content,
			ReasoningContent: response.ReasoningContent,
		}
		assistantMsg.ToolCalls = providers.CanonicalToolCalls(response.ToolCalls)

		if len(assistantMsg.ToolCalls) == 0 {
			logger.WarnCF("agent", "group turn returned only invalid tool calls",
				map[string]interface{}{
					"speaker":     req.Speaker,
					"group_id":    req.GroupID,
					"iteration":   iteration,
					"rejected":    len(response.ToolCalls),
					"session_key": sessionKey,
				})
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

		executor := newToolExecutor(lr.al)
		for _, tc := range assistantMsg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return "", totalTokens, err
			}

			argsJSON := ""
			if tc.Arguments != nil {
				if b, mErr := json.Marshal(tc.Arguments); mErr == nil {
					argsJSON = string(b)
				}
			}

			if req.OnToolCall != nil {
				req.OnToolCall(tc.ID, tc.Name, argsJSON, "executing", "")
			}

			toolResult, err := executor.Execute(toolExecOptions{
				ctx:        ctx,
				agent:      agent,
				registry:   registry,
				tc:         tc,
				channel:    req.OriginChannel,
				chatID:     req.OriginChatID,
				sessionKey: sessionKey,
				iteration:  iteration,
			})

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

			contentForLLM := lr.al.redactor.Redact(buildToolResultContent(toolResult))
			toolResultMsg := providers.Message{
				Role:       "tool",
				Content:    contentForLLM,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolResultMsg)

			if toolResult != nil && len(toolResult.ContextMessages) > 0 {
				ctxMsgs := toolResult.ContextMessages
				if !modelHasVision || !modelHasVideo {
					ctxMsgs = stripContentParts(ctxMsgs, !modelHasVision, !modelHasVideo)
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

	logger.WarnCF("agent", "Group turn tool loop exhausted, forcing final response",
		map[string]interface{}{
			"speaker":  req.Speaker,
			"group_id": req.GroupID,
		})

	callOpts := llmCallOptions{
		ctx:        ctx,
		agent:      agent,
		messages:   messages,
		toolDefs:   nil,
		model:      agent.Model,
		candidates: agent.Candidates,
		sessionKey: sessionKey,
		iteration:  maxGroupToolIterations + 1,
	}
	if reduced, evicted := tools.EnforceImageBudget(messages); evicted > 0 {
		messages = reduced
		logger.InfoCF("agent", "Group turn request image budget enforced", map[string]interface{}{
			"group_id": req.GroupID, "speaker": req.Speaker, "iteration": maxGroupToolIterations + 1,
			"evicted_parts": evicted,
		})
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
