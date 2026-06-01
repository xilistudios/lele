// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

const (
	// MaxLLMRetries is the maximum number of retries for LLM calls on context errors
	MaxLLMRetries = 2
)

// llmCallOptions aggregates all options for an LLM call
type llmCallOptions struct {
	ctx            context.Context
	agent          *AgentInstance
	messages       []providers.Message
	toolDefs       []providers.ToolDefinition
	model          string
	candidates     []providers.FallbackCandidate
	sessionKey     string
	channel        string
	chatID         string
	iteration      int
	streamOnChunk  func(chunk string, done bool)
	streamOnReason func(reasoningChunk string)
}

type streamingState struct {
	chunked bool
}

func (s *streamingState) onChunk(delegate func(chunk string, done bool)) func(chunk string, done bool) {
	return func(chunk string, done bool) {
		if chunk != "" {
			s.chunked = true
		}
		delegate(chunk, done)
	}
}

func (s *streamingState) onReasoning(delegate func(reasoningChunk string)) func(reasoningChunk string) {
	if delegate == nil {
		return nil
	}
	return func(reasoningChunk string) {
		if reasoningChunk != "" {
			s.chunked = true
		}
		delegate(reasoningChunk)
	}
}

// llmCaller handles communication with LLM providers including fallback and retry
type llmCaller struct {
	al *AgentLoop

	// retryWait is called to wait between retry attempts.
	// Defaults to time.After. Override in tests to avoid real sleeps.
	retryWait func(time.Duration) <-chan time.Time
}

// newLLMCaller creates a new LLM caller
func newLLMCaller(al *AgentLoop) *llmCaller {
	return &llmCaller{
		al:        al,
		retryWait: time.After,
	}
}

// buildLLMOptions constructs LLM options including reasoning and thinking config
func (lc *llmCaller) buildLLMOptions(opts llmCallOptions) map[string]interface{} {
	llmOptions := map[string]interface{}{
		"max_tokens":  opts.agent.MaxTokens,
		"temperature": opts.agent.Temperature,
	}

	// Add reasoning config with per-session override support
	sessionEffort := ""
	if opts.sessionKey != "" {
		if v, ok := lc.al.sessionThinking.Load(opts.sessionKey); ok {
			if s, ok := v.(string); ok {
				sessionEffort = s
			}
		}
		// Fallback: check persisted session (survives restarts)
		if sessionEffort == "" {
			agent := lc.al.agentForSession(opts.sessionKey)
			if agent != nil && agent.Sessions != nil {
				sessionEffort = agent.Sessions.GetThinkingLevel(opts.sessionKey)
			}
		}
	}

	if sessionEffort == "off" {
		// Explicitly disabled for this session – do not send reasoning.
	} else if sessionEffort != "" {
		reasoningMap := map[string]interface{}{
			"effort": sessionEffort,
		}
		// Merge other reasoning fields from agent config (if any)
		if opts.agent.Reasoning != nil {
			if opts.agent.Reasoning.MaxTokens != nil {
				reasoningMap["max_tokens"] = *opts.agent.Reasoning.MaxTokens
			}
			if opts.agent.Reasoning.Exclude != nil {
				reasoningMap["exclude"] = *opts.agent.Reasoning.Exclude
			}
			if opts.agent.Reasoning.Summary != nil {
				reasoningMap["summary"] = *opts.agent.Reasoning.Summary
			}
			if opts.agent.Reasoning.Enable {
				reasoningMap["enabled"] = true
			}
		}
		llmOptions["reasoning"] = reasoningMap
		logger.DebugCF("agent", "Session reasoning override applied", map[string]interface{}{
			"agent_id":    opts.agent.ID,
			"session_key": opts.sessionKey,
			"effort":      sessionEffort,
		})
	} else if opts.agent.Reasoning != nil {
		reasoningMap := map[string]interface{}{}
		if opts.agent.Reasoning.Effort != nil {
			reasoningMap["effort"] = *opts.agent.Reasoning.Effort
		}
		if opts.agent.Reasoning.MaxTokens != nil {
			reasoningMap["max_tokens"] = *opts.agent.Reasoning.MaxTokens
		}
		if opts.agent.Reasoning.Exclude != nil {
			reasoningMap["exclude"] = *opts.agent.Reasoning.Exclude
		}
		if opts.agent.Reasoning.Summary != nil {
			reasoningMap["summary"] = *opts.agent.Reasoning.Summary
		}
		if opts.agent.Reasoning.Enable {
			reasoningMap["enabled"] = true
		}
		if len(reasoningMap) > 0 {
			llmOptions["reasoning"] = reasoningMap
			logger.DebugCF("agent", "Reasoning config applied", map[string]interface{}{
				"agent_id":   opts.agent.ID,
				"effort":     opts.agent.Reasoning.Effort,
				"max_tokens": opts.agent.Reasoning.MaxTokens,
				"exclude":    opts.agent.Reasoning.Exclude,
				"summary":    opts.agent.Reasoning.Summary,
				"enable":     opts.agent.Reasoning.Enable,
			})
		}
	}

	// Enable thinking mode for DeepSeek models if reasoning is enabled.
	// For OpenRouter, thinking is handled via reasoning.enabled / reasoning.effort.
	if opts.agent.Reasoning != nil && opts.agent.Reasoning.Enable {
		if isDeepSeekModel(opts.model) {
			llmOptions["thinking"] = true
			logger.DebugCF("agent", "Thinking mode enabled for DeepSeek model",
				map[string]interface{}{
					"agent_id": opts.agent.ID,
					"model":    opts.model,
				})
		}
	}

	return llmOptions
}

// call performs the LLM call with optional streaming, fallback chain
func (lc *llmCaller) call(opts llmCallOptions) (*providers.LLMResponse, error) {
	llmOptions := lc.buildLLMOptions(opts)

	// Strip provider prefix from model for API calls.
	// The provider prefix (e.g., "openrouter:") is for internal routing only
	// and must not be sent to the external LLM API.
	apiModel := providers.StripProviderPrefix(opts.model)

	// Resolve the correct provider for this call.
	// When a session overrides the model to use a different provider,
	// the agent's default provider is stale — route to the provider
	// specified in the model string instead.
	callProvider := opts.agent.Provider
	if ref := providers.ParseModelRef(opts.model, ""); ref != nil && ref.Provider != "" {
		agentRef := providers.ParseModelRef(opts.agent.Model, "")
		if agentRef == nil || agentRef.Provider != ref.Provider {
			if newProv, err := providers.CreateProviderForCandidate(lc.al.cfg(), ref.Provider); err == nil {
				callProvider = newProv
			}
		}
	}

	// Try streaming if available and requested
	if opts.streamOnChunk != nil {
		if sp, ok := callProvider.(providers.StreamingLLMProvider); ok {
			state := &streamingState{}
			response, err := sp.ChatStream(
				opts.ctx,
				opts.messages,
				opts.toolDefs,
				apiModel,
				llmOptions,
				state.onChunk(opts.streamOnChunk),
				state.onReasoning(opts.streamOnReason),
			)
			shouldFallback := err != nil && !state.chunked && len(opts.candidates) > 0 && lc.al.fallback != nil
			if !shouldFallback {
				return response, err
			}
			logger.WarnCF("agent", "Streaming provider failed before producing chunks; trying fallback chain", map[string]interface{}{
				"agent_id": opts.agent.ID,
				"model":    apiModel,
				"error":    err.Error(),
			})
		}
	}

	// Execute with fallback chain if configured
	if len(opts.candidates) > 0 && lc.al.fallback != nil {
		return lc.callWithFallback(opts, llmOptions)
	}

	// Direct call without fallback
	return callProvider.Chat(opts.ctx, opts.messages, opts.toolDefs, apiModel, llmOptions)
}

// callWithFallback executes LLM call through the fallback chain
func (lc *llmCaller) callWithFallback(opts llmCallOptions, llmOptions map[string]interface{}) (*providers.LLMResponse, error) {
	fbResult, fbErr := lc.al.fallback.Execute(opts.ctx, opts.candidates,
		func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
			providerInst, err := providers.CreateProviderForCandidate(lc.al.cfg(), provider)
			if err != nil {
				if opts.agent.Provider != nil {
					return opts.agent.Provider.Chat(ctx, opts.messages, opts.toolDefs, model, llmOptions)
				}
				return nil, fmt.Errorf("no provider available for model %s", model)
			}
			// Use model directly - candidates already carry bare model names
			// (e.g., "deepseek/deepseek-v4-pro") resolved from ResolveModelAlias.
			// Do NOT wrap with FormatProviderModel — the "provider:model" colon
			// format is internal-only and would break the API call.
			return providerInst.Chat(ctx, opts.messages, opts.toolDefs, model, llmOptions)
		},
	)
	if fbErr != nil {
		return nil, fbErr
	}
	if fbResult.Provider != "" && len(fbResult.Attempts) > 0 {
		logger.InfoCF("agent", fmt.Sprintf("Fallback: succeeded with %s/%s after %d attempts",
			fbResult.Provider, fbResult.Model, len(fbResult.Attempts)+1),
			map[string]interface{}{"agent_id": opts.agent.ID, "iteration": opts.iteration})
	}
	return fbResult.Response, nil
}

// executeWithRetry performs LLM call with retry logic for context/token errors
// Returns the response and an updated messages slice (after potential summarization)
func (lc *llmCaller) executeWithRetry(
	opts llmCallOptions,
	messages []providers.Message,
) (*providers.LLMResponse, []providers.Message, error) {
	var response *providers.LLMResponse
	var err error
	currentMessages := messages

	for retry := 0; retry <= MaxLLMRetries; retry++ {
		opts.messages = currentMessages
		response, err = lc.call(opts)
		if err == nil {
			return response, currentMessages, nil
		}

		if opts.ctx.Err() != nil {
			return nil, currentMessages, opts.ctx.Err()
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
			case <-lc.retryWait(waitTime):
			case <-opts.ctx.Done():
				return nil, currentMessages, opts.ctx.Err()
			}
			continue
		}

		if isContextError && retry < MaxLLMRetries {
			logger.WarnCF("agent", "Context window error detected, attempting summarization", map[string]interface{}{
				"error": err.Error(),
				"retry": retry,
			})

			if retry == 0 && !constants.IsInternalChannel(opts.channel) {
				lc.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel: opts.channel,
					ChatID:  opts.chatID,
					Content: "Context window exceeded. Summarizing history and retrying...",
				})
			}

			// Summarize session to reduce context
			stats := lc.al.sessionManager.summarizeSession(opts.agent, opts.sessionKey)
			if stats == nil {
				logger.ErrorCF("agent", "Summarization failed, falling back to compression", nil)
				if sm, ok := lc.al.sessionManager.(*sessionManagerImpl); ok {
					sm.forceCompression(opts.agent, opts.sessionKey)
				}
			}

			// Rebuild messages with summarized history
			newHistory := opts.agent.Sessions.GetHistory(opts.sessionKey)
			newSummary := opts.agent.Sessions.GetSummary(opts.sessionKey)
			newHistory = ensureSummaryMaterialized(opts.agent, opts.sessionKey, newHistory, newSummary)
			currentMessages = opts.agent.ContextBuilder.BuildMessages(
				newHistory, newSummary, "",
				nil, opts.channel, opts.chatID, opts.sessionKey,
			)
			continue
		}
		break
	}

	return response, currentMessages, err
}
