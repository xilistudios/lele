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
	MaxLLMRetries = 2
)

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

type llmCaller struct {
	al *AgentLoop

	retryWait func(time.Duration) <-chan time.Time
}

func newLLMCaller(al *AgentLoop) *llmCaller {
	return &llmCaller{
		al:        al,
		retryWait: time.After,
	}
}

func (lc *llmCaller) buildLLMOptions(opts llmCallOptions) map[string]interface{} {
	llmOptions := map[string]interface{}{
		"max_tokens":  opts.agent.MaxTokens,
		"temperature": opts.agent.Temperature,
	}

	if tt := getThinkingType(lc.al.cfg(), opts.model, lc.al.cfg().Agents.Defaults.Provider); tt != "" {
		llmOptions["thinking_type"] = tt
	}

	if opts.agent.PromptCache.Enabled {
		llmOptions[providers.OptPromptCache] = true
		llmOptions[providers.OptPromptCacheTTL] = opts.agent.PromptCache.NormalizedTTL()
	}

	sessionEffort := ""
	if opts.sessionKey != "" {
		if v, ok := lc.al.sessionThinking.Load(opts.sessionKey); ok {
			if s, ok := v.(string); ok {
				sessionEffort = s
			}
		}
		if sessionEffort == "" {
			agent := lc.al.agentForSession(opts.sessionKey)
			if agent != nil && agent.Sessions != nil {
				sessionEffort = agent.Sessions.GetThinkingLevel(opts.sessionKey)
			}
		}
	}

	effort := sessionEffort
	if effort == "" {
		effort = opts.agent.ThinkingLevel
	}

	switch {
	case effort == "off":
		llmOptions["reasoning"] = map[string]interface{}{"enabled": false}
	case effort != "":
		reasoningMap := map[string]interface{}{
			"effort":  effort,
			"enabled": true,
		}
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
		}
		llmOptions["reasoning"] = reasoningMap
		source := "agent_config"
		if sessionEffort != "" {
			source = "session"
		}
		logger.DebugCF("agent", "Reasoning effort applied", map[string]interface{}{
			"agent_id":    opts.agent.ID,
			"session_key": opts.sessionKey,
			"effort":      effort,
			"source":      source,
		})
	case opts.agent.Reasoning != nil:
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

	if opts.agent.Reasoning != nil && opts.agent.Reasoning.Enable && effort != "off" {
		if isDeepSeekModel(opts.model) {
			llmOptions["thinking"] = true
			logger.DebugCF("agent", "Thinking mode enabled for DeepSeek model",
				map[string]interface{}{
					"agent_id": opts.agent.ID,
					"model":    opts.model,
				})
		}
	}

	llmOptions = providers.WithWireSessionID(llmOptions, opts.sessionKey)

	return llmOptions
}

func (lc *llmCaller) call(opts llmCallOptions) (*providers.LLMResponse, error) {
	if healed, changed := providers.HealToolCallPairs(opts.messages); changed {
		logger.WarnCF("agent", "Healed broken tool-call pairing before provider call",
			map[string]interface{}{
				"session_key": opts.sessionKey,
				"iteration":   opts.iteration,
				"before":      len(opts.messages),
				"after":       len(healed),
			})
		opts.messages = healed
	}

	llmOptions := lc.buildLLMOptions(opts)

	apiModel := providers.StripProviderPrefix(opts.model)

	callProvider := opts.agent.Provider
	if ref := providers.ParseModelRef(opts.model, ""); ref != nil && ref.Provider != "" {
		agentRef := providers.ParseModelRef(opts.agent.Model, "")
		if agentRef == nil || agentRef.Provider != ref.Provider {
			if newProv, err := providers.CreateProviderForCandidate(lc.al.cfg(), ref.Provider); err == nil {
				callProvider = newProv
			}
		}
	}

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

	if len(opts.candidates) > 0 && lc.al.fallback != nil {
		return lc.callWithFallback(opts, llmOptions)
	}

	return providers.ChatIdle(opts.ctx, callProvider, opts.messages, opts.toolDefs, apiModel, llmOptions)
}

func (lc *llmCaller) callWithFallback(opts llmCallOptions, llmOptions map[string]interface{}) (*providers.LLMResponse, error) {
	fbResult, fbErr := lc.al.fallback.Execute(opts.ctx, opts.candidates,
		func(ctx context.Context, provider, model string) (*providers.LLMResponse, error) {
			messages := opts.messages
			candidateModel := provider + ":" + model
			candidateHasImages := getSupportsImages(lc.al.cfg(), candidateModel, provider)
			candidateHasVideo := getSupportsVideo(lc.al.cfg(), candidateModel, provider)
			if !candidateHasImages || !candidateHasVideo {
				messages = stripContentParts(messages, !candidateHasImages, !candidateHasVideo)
			}

			providerInst, err := providers.CreateProviderForCandidate(lc.al.cfg(), provider)
			if err != nil {
				if opts.agent.Provider != nil {
					return providers.ChatIdle(ctx, opts.agent.Provider, messages, opts.toolDefs, model, llmOptions)
				}
				return nil, fmt.Errorf("no provider available for model %s", model)
			}
			return providers.ChatIdle(ctx, providerInst, messages, opts.toolDefs, model, llmOptions)
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
		isContextError := providers.IsContextOverflowError(err)
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
			if retry == 0 {
				if reduced, evicted := providers.ReduceImageParts(currentMessages, 0); evicted > 0 {
					logger.WarnCF("agent", "Request rejected, media evicted and retrying", map[string]interface{}{
						"error":         err.Error(),
						"retry":         retry,
						"session_key":   opts.sessionKey,
						"size_error":    providers.IsRequestSizeError(err),
						"evicted_parts": evicted,
						"request_bytes": providers.EstimateRequestBytes(reduced),
					})
					if opts.agent != nil && opts.agent.Sessions != nil && opts.sessionKey != "" {
						if _, pErr := opts.agent.Sessions.EvictImageContent(opts.sessionKey, 0); pErr != nil {
							logger.WarnCF("agent", "Failed to persist media eviction", map[string]interface{}{
								"session_key": opts.sessionKey,
								"error":       pErr.Error(),
							})
						}
					}
					currentMessages = reduced
					continue
				}
			}
			logger.WarnCF("agent", "Context window error detected, attempting summarization", map[string]interface{}{
				"error": err.Error(),
				"retry": retry,
			})

			if retry == 0 && !constants.IsInternalChannel(opts.channel) {
				if opts.channel == "native" {
					lc.al.publishToolLifecycle(bus.OutboundMessage{
						Channel: opts.channel,
						ChatID:  opts.chatID,
						Event:   "tool.executing",
						Metadata: map[string]string{
							"tool":   "compact",
							"action": "Context window exceeded. Compacting...",
						},
					})
				} else {
					lc.al.bus.PublishOutbound(bus.OutboundMessage{
						Channel: opts.channel,
						ChatID:  opts.chatID,
						Content: "Context window exceeded. Summarizing history and retrying...",
					})
				}
			}

			var stats *SummarizeStats
			if sm, ok := lc.al.sessionManager.(*sessionManagerImpl); ok {
				var ran bool
				stats, ran = sm.summarizeSessionGuarded(opts.agent, opts.sessionKey)
				if !ran {
					logger.WarnCF("agent", "Summarization already in progress for session; skipping duplicate reactive compaction", map[string]interface{}{
						"session_key": opts.sessionKey,
					})
				}
				if stats == nil {
					logger.ErrorCF("agent", "Summarization failed, falling back to compression", nil)
					sm.forceCompression(opts.agent, opts.sessionKey)
				}
			} else {
				stats = lc.al.sessionManager.summarizeSession(opts.agent, opts.sessionKey)
				if stats == nil {
					logger.ErrorCF("agent", "Summarization failed (no compression fallback available)", nil)
				}
			}

			newHistory := opts.agent.Sessions.GetHistory(opts.sessionKey)
			newSummary := opts.agent.Sessions.GetSummary(opts.sessionKey)
			newHistory = ensureSummaryMaterialized(opts.agent, opts.sessionKey, newHistory, newSummary)
			currentMessages = opts.agent.ContextBuilder.BuildMessages(
				newHistory, newSummary, "",
				nil, opts.channel, opts.chatID, opts.sessionKey,
				opts.agent.Sessions.GetMode(opts.sessionKey),
			)
			continue
		}
		break
	}

	return response, currentMessages, err
}
