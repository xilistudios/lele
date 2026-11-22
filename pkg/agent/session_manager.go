package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

type sessionCancelGroup struct {
	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc
}

func newSessionCancelGroup() *sessionCancelGroup {
	return &sessionCancelGroup{
		cancels: make(map[uint64]context.CancelFunc),
	}
}

func (scg *sessionCancelGroup) add(id uint64, cancel context.CancelFunc) {
	scg.mu.Lock()
	defer scg.mu.Unlock()
	scg.cancels[id] = cancel
}

func (scg *sessionCancelGroup) remove(id uint64) bool {
	scg.mu.Lock()
	defer scg.mu.Unlock()
	delete(scg.cancels, id)
	return len(scg.cancels) == 0
}

func (scg *sessionCancelGroup) cancelAll() int {
	scg.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(scg.cancels))
	for id, cancel := range scg.cancels {
		delete(scg.cancels, id)
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	scg.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	return len(cancels)
}

type sessionManagerImpl struct {
	al             *AgentLoop
	bus            *bus.MessageBus
	summarizing    *sync.Map
	sessionCancels sync.Map
	cancelSeq      atomic.Uint64
}

type sessionManager interface {
	maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats
	summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats
	summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error)
	AddTokenCounts(sessionKey string, inputTokens, outputTokens int)
	EstimateTokens(messages []providers.Message) int
	RegisterSessionCancel(sessionKey string, cancel context.CancelFunc) func()
	CancelSession(sessionKey string) int
	IsSessionProcessing(sessionKey string) bool
	ModelForSession(agent *AgentInstance, sessionKey string) string
}

func newSessionManager(al *AgentLoop) *sessionManagerImpl {
	return &sessionManagerImpl{
		al:          al,
		bus:         al.bus,
		summarizing: &al.summarizing,
	}
}

func (sm *sessionManagerImpl) RegisterSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	if sessionKey == "" || cancel == nil {
		return func() {}
	}

	id := sm.cancelSeq.Add(1)
	rawGroup, _ := sm.sessionCancels.LoadOrStore(sessionKey, newSessionCancelGroup())
	group, ok := rawGroup.(*sessionCancelGroup)
	if !ok || group == nil {
		group = newSessionCancelGroup()
		sm.sessionCancels.Store(sessionKey, group)
	}
	group.add(id, cancel)

	return func() {
		cancel()
		if !group.remove(id) {
			return
		}
		if current, ok := sm.sessionCancels.Load(sessionKey); ok && current == group {
			sm.sessionCancels.Delete(sessionKey)
		}
		sm.al.clearInProgressTool(sessionKey)
	}
}

func (sm *sessionManagerImpl) CancelSession(sessionKey string) int {
	if sessionKey == "" {
		logger.WarnCF("agent", "CancelSession called with empty session key", nil)
		return 0
	}

	rawGroup, ok := sm.sessionCancels.Load(sessionKey)
	if !ok {
		logger.WarnCF("agent", "CancelSession: no cancel group found for session", map[string]interface{}{
			"session_key": sessionKey,
		})
		return 0
	}

	logger.InfoCF("agent", "CancelSession: cancelling session", map[string]interface{}{
		"session_key": sessionKey,
	})

	switch entry := rawGroup.(type) {
	case *sessionCancelGroup:
		stopped := entry.cancelAll()
		sm.sessionCancels.Delete(sessionKey)
		sm.al.clearInProgressTool(sessionKey)
		logger.InfoCF("agent", "CancelSession: cancelled session group", map[string]interface{}{
			"session_key": sessionKey,
			"stopped":     stopped,
		})
		return stopped
	case context.CancelFunc:
		if entry != nil {
			entry()
		}
		sm.sessionCancels.Delete(sessionKey)
		sm.al.clearInProgressTool(sessionKey)
		logger.InfoCF("agent", "CancelSession: cancelled single session func", map[string]interface{}{
			"session_key": sessionKey,
		})
		return 1
	default:
		sm.sessionCancels.Delete(sessionKey)
		sm.al.clearInProgressTool(sessionKey)
		logger.WarnCF("agent", "CancelSession: unknown cancel entry type", map[string]interface{}{
			"session_key": sessionKey,
		})
		return 0
	}
}

func (sm *sessionManagerImpl) IsSessionProcessing(sessionKey string) bool {
	_, ok := sm.sessionCancels.Load(sessionKey)
	return ok
}

func (sm *sessionManagerImpl) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	newHistory := agent.Sessions.GetHistoryView(sessionKey)

	contextWindow := sm.al.getSessionContextWindow(sessionKey)

	if contextWindow <= 0 {
		logger.WarnCF("agent", "maybeSummarize skipped: no context window configured", map[string]interface{}{
			"session_key":             sessionKey,
			"agent_id":                agent.ID,
			"context_window":          agent.ContextWindow,
			"resolved_context_window": contextWindow,
		})
		return nil
	}

	historyTokens := sm.EstimateTokens(newHistory)

	summaryTokens := 0
	if summary := agent.Sessions.GetSummary(sessionKey); summary != "" {
		summaryTokens = sm.EstimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	systemPromptTokens := 0
	if agent.ContextBuilder != nil {
		systemPrompt := agent.ContextBuilder.BuildSystemPromptForSessionWithFolder(sessionKey, channel)
		systemPromptTokens = sm.EstimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})
	}

	tokenEstimate := systemPromptTokens + summaryTokens + historyTokens
	thresholdPercent := sm.al.cfg().SessionCompactionThresholdPercent()
	threshold := contextWindow * thresholdPercent / 100

	logger.InfoCF("agent", "maybeSummarize check", map[string]interface{}{
		"session_key":             sessionKey,
		"agent_id":                agent.ID,
		"context_window":          agent.ContextWindow,
		"resolved_context_window": contextWindow,
		"threshold":               threshold,
		"threshold_percent":       thresholdPercent,
		"token_estimate":          tokenEstimate,
		"system_prompt_tokens":    systemPromptTokens,
		"summary_tokens":          summaryTokens,
		"history_tokens":          historyTokens,
		"history_count":           len(newHistory),
	})

	if tokenEstimate > threshold {
		logger.InfoCF("agent", "Summarization triggered", map[string]interface{}{
			"session_key":             sessionKey,
			"agent_id":                agent.ID,
			"token_estimate":          tokenEstimate,
			"threshold":               threshold,
			"context_window":          agent.ContextWindow,
			"resolved_context_window": contextWindow,
		})
		if channel == "native" {
			sm.al.publishToolLifecycle(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Event:   "tool.executing",
				Metadata: map[string]string{
					"tool":   "compact",
					"action": "Compacting context...",
				},
			})
		}
		stats, ran := sm.summarizeSessionGuarded(agent, sessionKey)
		if !ran {
			logger.WarnCF("agent", "maybeSummarize: summarization already in progress, skipping", map[string]interface{}{
				"session_key": sessionKey,
				"agent_id":    agent.ID,
			})
			return nil
		}
		if stats == nil {
			logger.ErrorCF("agent", "maybeSummarize: summarization failed, falling back to forced compression", map[string]interface{}{
				"session_key": sessionKey,
				"agent_id":    agent.ID,
			})
			sm.forceCompression(agent, sessionKey)
			return nil
		}
		if channel == "native" {
			resultText := fmt.Sprintf("Messages: %d → %d (dropped %d), Tokens: ~%d → ~%d (saved ~%d)",
				stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
				stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens)
			sm.al.publishToolLifecycle(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Event:   "tool.result",
				Metadata: map[string]string{
					"tool":   "compact",
					"result": resultText,
				},
			})
		} else if !constants.IsInternalChannel(channel) {
			sm.bus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: fmt.Sprintf("📊 Memory optimized:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
					stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
					stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens),
			})
		}
		return stats
	}
	return nil
}

func (sm *sessionManagerImpl) summarizeSessionGuarded(agent *AgentInstance, sessionKey string) (*SummarizeStats, bool) {
	if agent == nil {
		return nil, false
	}
	summarizeKey := agent.ID + ":" + sessionKey
	if _, loading := sm.summarizing.LoadOrStore(summarizeKey, true); loading {
		return nil, false
	}
	defer sm.summarizing.Delete(summarizeKey)
	stats := sm.summarizeSession(agent, sessionKey)
	return stats, true
}

func (sm *sessionManagerImpl) summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats {
	stats, err := sm.summarizeSessionCore(agent, sessionKey)
	if err != nil {
		logger.WarnCF("agent", "summarizeSession: core summarization returned error", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return nil
	}
	return stats
}

func (sm *sessionManagerImpl) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistoryView(sessionKey)
	if len(history) <= 4 {
		return
	}

	conversation := history[:len(history)-1]
	if len(conversation) == 0 {
		return
	}

	mid := len(conversation) / 2

	droppedCount := mid

	if agent.Sessions.GetSummary(sessionKey) == "" {
		localSummary := "## Conversation Summary (auto-compressed)\n\n"
		localSummary += fmt.Sprintf("Messages compressed: %d of %d\n\n", mid, len(history))

		toolNames := make(map[string]struct{})
		maxSummaryMsgs := 20
		count := 0
		for i := 0; i < mid && count < maxSummaryMsgs; i++ {
			msg := conversation[i]
			text := msg.TextOnlyContent()
			if text == "" {
				continue
			}
			switch msg.Role {
			case "user":
				content := text
				if len(content) > 150 {
					content = content[:150] + "..."
				}
				localSummary += fmt.Sprintf("User: %s\n", content)
			case "assistant":
				content := text
				if len(content) > 150 {
					content = content[:150] + "..."
				}
				localSummary += fmt.Sprintf("Assistant: %s\n", content)
				for _, tc := range msg.ToolCalls {
					if tc.Function != nil && tc.Function.Name != "" {
						toolNames[tc.Function.Name] = struct{}{}
					}
				}
			case "tool":
				content := text
				if len(content) > 100 {
					content = content[:100] + "..."
				}
				localSummary += fmt.Sprintf("Tool result (%s): %s\n", msg.ToolCallID, content)
			}
			count++
		}

		if len(toolNames) > 0 {
			tools := make([]string, 0, len(toolNames))
			for name := range toolNames {
				tools = append(tools, name)
			}
			localSummary += fmt.Sprintf("\nTools used: %s\n", strings.Join(tools, ", "))
		}

		agent.Sessions.SetSummary(sessionKey, localSummary)
	}

	agent.Sessions.ExcludeOldMessagesFromContext(sessionKey, len(history)-mid)
	agent.Sessions.IncrementCompactionCount(sessionKey)
	if err := agent.Sessions.Save(sessionKey); err != nil {
		logger.WarnCF("agent", "Failed to save after forced compression", map[string]interface{}{"session_key": sessionKey, "error": err.Error()})
	} else {
		sm.evictExcludedAfterCompaction(agent, sessionKey)
	}

	logger.WarnCF("agent", "Forced compression executed", map[string]interface{}{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"total_msgs":   len(history),
	})
}

func (sm *sessionManagerImpl) evictExcludedAfterCompaction(agent *AgentInstance, sessionKey string) {
	if !sm.al.cfg().EvictExcludedFromMemory() {
		return
	}
	agent.Sessions.EvictExcludedMessages(sessionKey)
}

func truncateUTF8Safe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	idx := maxBytes
	for idx > 0 && (s[idx]&0xC0) == 0x80 {
		idx--
	}
	return s[:idx]
}

const videoPartEstimateChars = providers.VideoPartCharCost

func (sm *sessionManagerImpl) EstimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		if m.ExcludeFromContext {
			continue
		}
		totalChars += providers.MessageCharCost(m)
	}
	return totalChars * 2 / 5
}

func (sm *sessionManagerImpl) ModelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		resolvedSessionKey := sm.al.ResolveSessionKey(sessionKey)
		if model, ok := sm.al.sessionModels.Load(resolvedSessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
			}
		}
		if agent.Sessions != nil {
			persistedModel := agent.Sessions.GetModel(resolvedSessionKey)
			if persistedModel != "" {
				return sm.al.cfg().Providers.ResolveModelAlias(persistedModel, sm.al.cfg().Agents.Defaults.Provider)
			}
		}
	}
	return agent.Model
}

func (sm *sessionManagerImpl) AddTokenCounts(sessionKey string, inputTokens, outputTokens int) {
	var agent *AgentInstance
	if overrideID := sm.al.getSessionAgent(sessionKey); overrideID != "" {
		if a, ok := sm.al.registry.GetAgent(overrideID); ok {
			agent = a
		}
	}

	if agent == nil {
		parsed := routing.ParseAgentSessionKey(sessionKey)
		if parsed != nil {
			a, ok := sm.al.registry.GetAgent(parsed.AgentID)
			if !ok || a == nil {
				return
			}
			agent = a
		} else {
			agent = sm.al.registry.GetDefaultAgent()
			if agent == nil {
				return
			}
		}
	}

	agent.Sessions.AddTokenCounts(sessionKey, inputTokens, outputTokens)
}

func (sm *sessionManagerImpl) summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	return sm.summarizeSessionCore(agent, sessionKey)
}

func (sm *sessionManagerImpl) summarizeSessionCore(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	if agent == nil || agent.Provider == nil {
		return nil, fmt.Errorf("no provider available for summarization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistoryView(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)
	historyForSummary := stripSummaryMessages(history)
	historyForSummary = filterContextMessages(historyForSummary)

	if len(historyForSummary) <= 2 {
		return nil, fmt.Errorf("not enough messages to summarize (need at least 3, have %d)", len(historyForSummary))
	}

	beforeMessages := len(history)
	beforeTokens := sm.EstimateTokens(history)

	toSummarize := historyForSummary[:len(historyForSummary)-2]

	if len(toSummarize) == 0 {
		return nil, fmt.Errorf("no messages available to summarize")
	}

	contextWindow := sm.al.getSessionContextWindow(sessionKey)
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	maxPromptChars := contextWindow * 2 / 5 * 40 / 100
	perMessageLimit := 2000

	prompt := "Please provide a comprehensive summary of the following conversation. " +
		"Capture all important context, decisions, facts, and action items so that " +
		"someone reading just this summary would understand what happened.\n\n"

	if existingSummary != "" {
		prompt += "=== PREVIOUS SUMMARY ===\n" + existingSummary + "\n\n"
	}

	prompt += "=== CONVERSATION TO SUMMARIZE ===\n"
	messagesIncluded := 0
	messagesSkipped := 0
	for _, m := range toSummarize {
		role := strings.ToUpper(m.Role)
		content := m.TextOnlyContent()
		if content == "" {
			continue
		}
		if len(content) > perMessageLimit {
			content = truncateUTF8Safe(content, perMessageLimit) + "\n[Content truncated...]"
		}
		entry := fmt.Sprintf("%s: %s\n\n", role, content)
		if len(prompt)+len(entry) > maxPromptChars && messagesIncluded > 0 {
			messagesSkipped++
			continue
		}
		prompt += entry
		messagesIncluded++
	}
	if messagesSkipped > 0 {
		prompt += fmt.Sprintf("[Note: %d additional messages were skipped due to size limits]\n\n", messagesSkipped)
	}

	prompt += "=== END OF CONVERSATION ===\n\n" +
		"Now provide a detailed summary that preserves all critical context."

	summarizeModel := sm.al.sessionManager.ModelForSession(agent, sessionKey)
	if cm := sm.al.cfg().CompactionModel(); cm != "" {
		summarizeModel = cm
	}
	summarizeProvider := agent.Provider

	if ref := providers.ParseModelRef(summarizeModel, ""); ref != nil && ref.Provider != "" {
		agentRef := providers.ParseModelRef(agent.Model, "")
		if agentRef == nil || agentRef.Provider != ref.Provider {
			if newProv, err := providers.CreateProviderForCandidate(sm.al.cfg(), ref.Provider); err == nil {
				summarizeProvider = newProv
			}
		}
	}

	apiModel := providers.StripProviderPrefix(summarizeModel)

	summarizeOpts := map[string]interface{}{}

	cfg := sm.al.cfg()
	providerName := extractProviderFromModel(summarizeModel, cfg.Agents.Defaults.Provider)
	if mc, ok := getProviderModelConfig(cfg, summarizeModel, providerName); ok {
		maxTokens := 2048
		if mc.MaxTokens > 0 {
			maxTokens = mc.MaxTokens
		}
		summarizeOpts["max_tokens"] = maxTokens

		if mc.Reasoning != nil && mc.Reasoning.Enable {
			reasoningMap := map[string]interface{}{
				"enabled": true,
			}
			if mc.Reasoning.Effort != nil {
				reasoningMap["effort"] = *mc.Reasoning.Effort
			}
			if mc.Reasoning.MaxTokens != nil {
				reasoningMap["max_tokens"] = *mc.Reasoning.MaxTokens
			}
			summarizeOpts["reasoning"] = reasoningMap
		} else if mc.Temperature != nil {
			summarizeOpts["temperature"] = *mc.Temperature
		} else {
			summarizeOpts["temperature"] = 0.3
		}
	} else {
		summarizeOpts["max_tokens"] = 2048
		summarizeOpts["temperature"] = 0.3
	}

	summarizeOpts = providers.WithWireSessionID(summarizeOpts, sessionKey)

	resp, err := providers.ChatIdle(ctx, summarizeProvider, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, summarizeOpts)

	if err != nil && strings.Contains(strings.ToLower(err.Error()), "temperature") {
		delete(summarizeOpts, "temperature")
		resp, err = providers.ChatIdle(ctx, summarizeProvider, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, summarizeOpts)
	}

	var finalSummary string
	if err != nil {
		return nil, fmt.Errorf("LLM summarization failed: %w", err)
	}

	if resp != nil {
		finalSummary = resp.Content
	} else if existingSummary != "" {
		finalSummary = existingSummary + "\n[Update: Additional conversation not summarized due to empty response]"
	}

	if finalSummary == "" {
		return nil, fmt.Errorf("summarization produced empty result")
	}

	agent.Sessions.SetSummary(sessionKey, finalSummary)
	keepCount := 2
	if len(historyForSummary) <= 2 {
		keepCount = 0
	}
	agent.Sessions.ExcludeOldMessagesFromContext(sessionKey, keepCount)

	historyAfter := agent.Sessions.GetHistory(sessionKey)
	needsSave := false
	lastSummaryIdx := -1
	for i := range historyAfter {
		if isSummaryMessage(historyAfter[i]) {
			lastSummaryIdx = i
		}
	}
	if lastSummaryIdx >= 0 && historyAfter[lastSummaryIdx].ExcludeFromContext {
		historyAfter[lastSummaryIdx].ExcludeFromContext = false
		needsSave = true
	}
	if needsSave {
		agent.Sessions.SetHistory(sessionKey, historyAfter)
	}

	agent.Sessions.IncrementCompactionCount(sessionKey)
	if err := agent.Sessions.Save(sessionKey); err != nil {
		return nil, fmt.Errorf("save after summarization: %w", err)
	}
	sm.evictExcludedAfterCompaction(agent, sessionKey)

	afterHistory := agent.Sessions.GetHistoryView(sessionKey)
	contextAfter := countContextMessages(afterHistory)
	afterTokens := sm.EstimateTokens(afterHistory)

	return &SummarizeStats{
		BeforeMessages:  beforeMessages,
		AfterMessages:   contextAfter,
		DroppedMessages: beforeMessages - contextAfter,
		BeforeTokens:    beforeTokens,
		AfterTokens:     afterTokens,
		SavedTokens:     beforeTokens - afterTokens,
	}, nil
}

func countContextMessages(history []providers.Message) int {
	count := 0
	for _, msg := range history {
		if !msg.ExcludeFromContext {
			count++
		}
	}
	return count
}
