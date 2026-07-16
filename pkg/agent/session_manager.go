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
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

// sessionCancelGroup manages multiple cancel functions for a single session.
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

// sessionManagerImpl implements the sessionManager interface for managing
// session summarization, compression, token estimation, and cancellation.
type sessionManagerImpl struct {
	al             *AgentLoop
	bus            *bus.MessageBus
	summarizing    *sync.Map
	sessionCancels sync.Map // sessionKey -> context.CancelFunc
	cancelSeq      atomic.Uint64
}

// sessionManager is the internal interface for session management operations.
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

// newSessionManager creates a new session manager instance.
func newSessionManager(al *AgentLoop) *sessionManagerImpl {
	return &sessionManagerImpl{
		al:          al,
		bus:         al.bus,
		summarizing: &al.summarizing,
	}
}

// RegisterSessionCancel registers a cancel function for a session and returns a cleanup function.
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
	}
}

// CancelSession cancels all active processing for a session and returns the count of stopped operations.
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
		logger.InfoCF("agent", "CancelSession: cancelled single session func", map[string]interface{}{
			"session_key": sessionKey,
		})
		return 1
	default:
		sm.sessionCancels.Delete(sessionKey)
		logger.WarnCF("agent", "CancelSession: unknown cancel entry type", map[string]interface{}{
			"session_key": sessionKey,
		})
		return 0
	}
}

// IsSessionProcessing returns true if there is an active LLM processing loop for the session.
func (sm *sessionManagerImpl) IsSessionProcessing(sessionKey string) bool {
	_, ok := sm.sessionCancels.Load(sessionKey)
	return ok
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// Returns statistics about the compaction if it was triggered.
//
// The token estimate includes system prompt + summary + history to match the
// actual context sent to the LLM (and shown by /status). Without this,
// the compaction would only trigger based on history tokens, ignoring the
// potentially large system prompt.
func (sm *sessionManagerImpl) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	newHistory := agent.Sessions.GetHistory(sessionKey)

	// Resolve the effective context window for this session, honoring session
	// model overrides and provider model config (falls back to agent.ContextWindow
	// then to 128000).
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

	// Calculate total context tokens including system prompt and summary,
	// matching what formatStatusResponse and the actual LLM request use.
	historyTokens := sm.EstimateTokens(newHistory)

	summaryTokens := 0
	if summary := agent.Sessions.GetSummary(sessionKey); summary != "" {
		summaryTokens = sm.EstimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	systemPromptTokens := 0
	if agent.ContextBuilder != nil {
		systemPrompt := agent.ContextBuilder.BuildSystemPromptForSession(sessionKey, channel)
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
		if !constants.IsInternalChannel(channel) {
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

// summarizeSessionGuarded runs summarizeSession under the shared `summarizing`
// guard so that proactive (maybeSummarize) and reactive (context-window error)
// compaction of the same session can never run concurrently. If a summarization
// for the same agent+session is already in progress, it returns (nil, false)
// without doing anything. Otherwise it runs the summarization and returns
// (stats, true).
func (sm *sessionManagerImpl) summarizeSessionGuarded(agent *AgentInstance, sessionKey string) (*SummarizeStats, bool) {
	if agent == nil {
		return nil, false
	}
	summarizeKey := agent.ID + ":" + sessionKey
	if _, loading := sm.summarizing.LoadOrStore(summarizeKey, true); loading {
		// Another summarization is already running for this session.
		return nil, false
	}
	defer sm.summarizing.Delete(summarizeKey)
	stats := sm.summarizeSession(agent, sessionKey)
	return stats, true
}

// summarizeSession summarizes the conversation history for a session.
// It never returns an error; on failure it logs the cause and returns nil,
// leaving the session state untouched.
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

// forceCompression marks old messages as excluded from context when the limit is hit.
// It marks the oldest 50% of messages (keeping the last user message included).
func (sm *sessionManagerImpl) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// history contains only user/assistant/tool messages — no system prompt.
	// Mark the oldest half of the conversation as excluded, preserving the last message.
	conversation := history[:len(history)-1]
	if len(conversation) == 0 {
		return
	}

	mid := len(conversation) / 2

	droppedCount := mid

	// Mark the oldest 'mid' messages as excluded from context
	// (they remain in storage for the web UI)
	agent.Sessions.ExcludeOldMessagesFromContext(sessionKey, len(history)-mid)
	agent.Sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]interface{}{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"total_msgs":   len(history),
	})
}

// truncateUTF8Safe returns s truncated to at most maxBytes bytes, cutting at a
// valid UTF-8 rune boundary so the result is always valid UTF-8.
func truncateUTF8Safe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards to find a valid start byte (not a continuation byte).
	idx := maxBytes
	for idx > 0 && (s[idx]&0xC0) == 0x80 {
		idx--
	}
	return s[:idx]
}

// EstimateTokens estimates the number of tokens in a message list.
// Uses a safe heuristic of 2.5 characters per token to account for CJK and other
// overheads better than the previous 3 chars/token.
// Counts Content, ReasoningContent, ToolCall function names and arguments,
// and a fixed per-message structural overhead (10 chars ≈ 4 tokens).
// Messages marked with ExcludeFromContext are skipped.
func (sm *sessionManagerImpl) EstimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		if m.ExcludeFromContext {
			continue
		}
		totalChars += utf8.RuneCountInString(m.Content)
		totalChars += utf8.RuneCountInString(m.ReasoningContent)
		for _, tc := range m.ToolCalls {
			if tc.Function != nil {
				totalChars += utf8.RuneCountInString(tc.Function.Name)
				totalChars += utf8.RuneCountInString(tc.Function.Arguments)
			}
		}
		// Per-message structural overhead (~4 tokens via 2.5 chars/token heuristic)
		totalChars += 10
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}

// ModelForSession returns the model to use for a session.
func (sm *sessionManagerImpl) ModelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		resolvedSessionKey := sm.al.ResolveSessionKey(sessionKey)
		// First check in-memory override (fast path)
		if model, ok := sm.al.sessionModels.Load(resolvedSessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
			}
		}
		// Then check persisted session model (survives restarts)
		if agent.Sessions != nil {
			persistedModel := agent.Sessions.GetModel(resolvedSessionKey)
			if persistedModel != "" {
				return persistedModel
			}
		}
	}
	return agent.Model
}

// AddTokenCounts adds token counts to a session.
// It respects session agent overrides set via /agent command.
func (sm *sessionManagerImpl) AddTokenCounts(sessionKey string, inputTokens, outputTokens int) {
	// Check for session-level agent override first (set via /agent command)
	var agent *AgentInstance
	if overrideID := sm.al.getSessionAgent(sessionKey); overrideID != "" {
		if a, ok := sm.al.registry.GetAgent(overrideID); ok {
			agent = a
		}
	}

	// Fall back to agent ID embedded in the session key
	if agent == nil {
		parsed := routing.ParseAgentSessionKey(sessionKey)
		if parsed != nil {
			a, ok := sm.al.registry.GetAgent(parsed.AgentID)
			if !ok || a == nil {
				return
			}
			agent = a
		} else {
			// Session key doesn't have agent prefix (e.g., "telegram:12345")
			// Use the default agent
			agent = sm.al.registry.GetDefaultAgent()
			if agent == nil {
				return
			}
		}
	}

	agent.Sessions.AddTokenCounts(sessionKey, inputTokens, outputTokens)
}

// summarizeSessionWithError summarizes the conversation history for a session
// and returns any error. Delegates to summarizeSessionCore.
func (sm *sessionManagerImpl) summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	return sm.summarizeSessionCore(agent, sessionKey)
}

// summarizeSessionCore performs the actual summarization work shared by
// summarizeSession and summarizeSessionWithError. It returns statistics on
// success. On any failure it returns a non-nil error describing the cause.
// It never applies the "reuse existing summary with error note" fallback —
// that policy decision is left to the callers.
func (sm *sessionManagerImpl) summarizeSessionCore(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	if agent == nil || agent.Provider == nil {
		return nil, fmt.Errorf("no provider available for summarization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)
	historyForSummary := stripSummaryMessages(history)
	// Filter out messages already excluded from context (from previous
	// summarization or forceCompression). They should not be re-summarized.
	historyForSummary = filterContextMessages(historyForSummary)

	// Need at least 3 messages to summarize (keep last 2 for continuity)
	if len(historyForSummary) <= 2 {
		return nil, fmt.Errorf("not enough messages to summarize (need at least 3, have %d)", len(historyForSummary))
	}

	// Calculate before stats
	beforeMessages := len(history)
	beforeTokens := sm.EstimateTokens(history)

	// Summarize everything except the last 2 messages (kept for continuity).
	// Note: history contains only user/assistant/tool messages — no system prompt.
	toSummarize := historyForSummary[:len(historyForSummary)-2]

	if len(toSummarize) == 0 {
		return nil, fmt.Errorf("no messages available to summarize")
	}

	// Build comprehensive summary prompt with old messages.
	// Limit the total prompt size to avoid exceeding the context window
	// when there are many messages (which would cause the summarization
	// LLM call itself to fail).
	contextWindow := sm.al.getSessionContextWindow(sessionKey)
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	// Target ~40% of context window for the summarization prompt.
	// At 2.5 chars/token, this is contextWindow * 2 / 5 * 40 / 100 chars.
	maxPromptChars := contextWindow * 2 / 5 * 40 / 100
	// Per-message truncation limit (reduced from 4000 to 2000 to fit more messages)
	perMessageLimit := 2000

	prompt := "Please provide a comprehensive summary of the following conversation. " +
		"Capture all important context, decisions, facts, and action items so that " +
		"someone reading just this summary would understand what happened.\n\n"

	if existingSummary != "" {
		prompt += "=== PREVIOUS SUMMARY ===\n" + existingSummary + "\n\n"
	}

	prompt += "=== CONVERSATION TO SUMMARIZE ===\n"
	// Add messages from oldest to newest, respecting the total prompt size limit.
	// If we can't fit all messages, include as many as possible (from the oldest)
	// and note that some were skipped.
	messagesIncluded := 0
	messagesSkipped := 0
	for _, m := range toSummarize {
		role := strings.ToUpper(m.Role)
		content := m.Content
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

	// Call LLM to summarize everything.
	// Use the session's current model (which may differ from the agent's default
	// if the user changed it via /model command).
	summarizeModel := sm.al.sessionManager.ModelForSession(agent, sessionKey)
	summarizeProvider := agent.Provider

	// If the session model uses a different provider prefix, resolve the
	// correct provider (same logic as llm_caller.go).
	if ref := providers.ParseModelRef(summarizeModel, ""); ref != nil && ref.Provider != "" {
		agentRef := providers.ParseModelRef(agent.Model, "")
		if agentRef == nil || agentRef.Provider != ref.Provider {
			if newProv, err := providers.CreateProviderForCandidate(sm.al.cfg(), ref.Provider); err == nil {
				summarizeProvider = newProv
			}
		}
	}

	// Strip provider prefix for the API call (same as llm_caller.go)
	apiModel := providers.StripProviderPrefix(summarizeModel)

	resp, err := summarizeProvider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, apiModel, map[string]interface{}{
		"max_tokens":  2048,
		"temperature": 0.3,
	})

	var finalSummary string
	if err != nil {
		return nil, fmt.Errorf("LLM summarization failed: %w", err)
	}

	if resp != nil {
		finalSummary = resp.Content
	} else if existingSummary != "" {
		// Fall back to existing summary
		finalSummary = existingSummary + "\n[Update: Additional conversation not summarized due to empty response]"
	}

	if finalSummary == "" {
		return nil, fmt.Errorf("summarization produced empty result")
	}

	agent.Sessions.SetSummary(sessionKey, finalSummary)
	// Mark old messages as excluded from context instead of deleting them.
	keepCount := 2
	if len(historyForSummary) <= 2 {
		keepCount = 0
	}
	agent.Sessions.ExcludeOldMessagesFromContext(sessionKey, keepCount)

	// Ensure summary messages are never excluded from context.
	historyAfter := agent.Sessions.GetHistory(sessionKey)
	needsSave := false
	for i := range historyAfter {
		if isSummaryMessage(historyAfter[i]) && historyAfter[i].ExcludeFromContext {
			historyAfter[i].ExcludeFromContext = false
			needsSave = true
		}
	}
	if needsSave {
		agent.Sessions.SetHistory(sessionKey, historyAfter)
	}

	agent.Sessions.Save(sessionKey)

	// Calculate after stats
	afterHistory := agent.Sessions.GetHistory(sessionKey)
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
