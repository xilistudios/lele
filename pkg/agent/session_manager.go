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
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
)

// sessionManagerImpl implements the sessionManager interface for managing
// session summarization, compression, and token estimation.
type sessionManagerImpl struct {
	al          *AgentLoop
	bus         *bus.MessageBus
	summarizing *sync.Map
}

// sessionManager is the internal interface for session management operations.
// SummarizeStats is defined in loop.go
type sessionManager interface {
	maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats
	summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats
	summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error)
	AddTokenCounts(sessionKey string, inputTokens, outputTokens int)
}

// newSessionManager creates a new session manager instance.
func newSessionManager(al *AgentLoop) *sessionManagerImpl {
	return &sessionManagerImpl{
		al:          al,
		bus:         al.bus,
		summarizing: &al.summarizing,
	}
}

// maybeSummarize triggers summarization if the session history exceeds thresholds.
// Returns statistics about the compaction if it was triggered.
func (sm *sessionManagerImpl) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	newHistory := agent.Sessions.GetHistory(sessionKey)
	tokenEstimate := sm.estimateTokens(newHistory)
	threshold := agent.ContextWindow * 75 / 100

	// Only trigger based on token estimate, not message count
	if tokenEstimate > threshold {
		summarizeKey := agent.ID + ":" + sessionKey
		if _, loading := sm.summarizing.LoadOrStore(summarizeKey, true); !loading {
			stats := sm.summarizeSession(agent, sessionKey)
			sm.summarizing.Delete(summarizeKey)

			if !constants.IsInternalChannel(channel) && stats != nil {
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
	}
	return nil
}

// summarizeSession summarizes the conversation history for a session.
// Passes ALL old messages to the LLM with instructions to create a comprehensive summary.
// Returns statistics about the operation.
func (sm *sessionManagerImpl) summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats {
	if agent == nil || agent.Provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)

	// Need at least 3 messages to summarize (keep last 2 for continuity)
	if len(history) <= 2 {
		return nil
	}

	// Calculate before stats
	beforeMessages := len(history)
	beforeTokens := sm.estimateTokens(history)

	// Summarize everything except the last 2 messages (kept for continuity).
	// Note: history contains only user/assistant/tool messages — no system prompt.
	toSummarize := history[:len(history)-2]

	if len(toSummarize) == 0 {
		return nil
	}

	// Build comprehensive summary prompt with ALL old messages
	prompt := "Please provide a comprehensive summary of the following conversation. " +
		"Capture all important context, decisions, facts, and action items so that " +
		"someone reading just this summary would understand what happened.\n\n"

	if existingSummary != "" {
		prompt += "=== PREVIOUS SUMMARY ===\n" + existingSummary + "\n\n"
	}

	prompt += "=== CONVERSATION TO SUMMARIZE ===\n"
	for _, m := range toSummarize {
		role := strings.ToUpper(m.Role)
		content := m.Content
		// Truncate very long messages for the summary prompt
		if len(content) > 4000 {
			content = content[:4000] + "\n[Content truncated...]"
		}
		prompt += fmt.Sprintf("%s: %s\n\n", role, content)
	}

	prompt += "=== END OF CONVERSATION ===\n\n" +
		"Now provide a detailed summary that preserves all critical context."

	// Call LLM to summarize everything
	resp, err := agent.Provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, agent.Model, map[string]interface{}{
		"max_tokens":  2048,
		"temperature": 0.3,
	})

	var finalSummary string
	if err == nil && resp != nil {
		finalSummary = resp.Content
	} else if existingSummary != "" {
		// Fall back to existing summary
		finalSummary = existingSummary + "\n[Update: Additional conversation not summarized due to error]"
	}

	if finalSummary == "" {
		return nil
	}

	agent.Sessions.SetSummary(sessionKey, finalSummary)
	// Keep only the last 2 messages (the ones not summarized above)
	agent.Sessions.TruncateHistory(sessionKey, 2)
	agent.Sessions.Save(sessionKey)

	// Calculate after stats
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	afterMessages := len(afterHistory)
	afterTokens := sm.estimateTokens(afterHistory)

	return &SummarizeStats{
		BeforeMessages:  beforeMessages,
		AfterMessages:   afterMessages,
		DroppedMessages: beforeMessages - afterMessages,
		BeforeTokens:    beforeTokens,
		AfterTokens:     afterTokens,
		SavedTokens:     beforeTokens - afterTokens,
	}
}

// summarizeBatch summarizes a batch of messages.
func (sm *sessionManagerImpl) summarizeBatch(ctx context.Context, agent *AgentInstance, batch []providers.Message, existingSummary string) (string, error) {
	prompt := "Provide a concise summary of this conversation segment, preserving core context and key points.\n"
	if existingSummary != "" {
		prompt += "Existing context: " + existingSummary + "\n"
	}
	prompt += "\nCONVERSATION:\n"
	for _, m := range batch {
		prompt += fmt.Sprintf("%s: %s\n", m.Role, m.Content)
	}

	response, err := agent.Provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, agent.Model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		return "", err
	}
	return response.Content, nil
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest 50% of messages (keeping the last user message).
func (sm *sessionManagerImpl) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// history contains only user/assistant/tool messages — no system prompt.
	// Drop the oldest half of the conversation, preserving the last message.
	conversation := history[:len(history)-1]
	if len(conversation) == 0 {
		return
	}

	// Helper to find the mid-point of the conversation
	mid := len(conversation) / 2

	// New history structure:
	// 1. System Prompt
	// 2. [Summary of dropped part] - synthesized
	// 3. Second half of conversation
	// 4. Last message

	// Simplified approach for emergency: Drop first half of conversation
	// and rely on existing summary if present, or create a placeholder.

	droppedCount := mid
	keptConversation := conversation[mid:]

	// The summary is stored separately in session.Summary, so it persists.
	// We only modify the messages list here.
	newHistory := make([]providers.Message, 0, len(keptConversation)+1)
	newHistory = append(newHistory, keptConversation...)
	newHistory = append(newHistory, history[len(history)-1]) // Last message

	// Update session
	agent.Sessions.SetHistory(sessionKey, newHistory)
	agent.Sessions.Save(sessionKey)

	logger.WarnCF("agent", "Forced compression executed", map[string]interface{}{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(newHistory),
	})
}

// estimateTokens estimates the number of tokens in a message list.
// Uses a safe heuristic of 2.5 characters per token to account for CJK and other
// overheads better than the previous 3 chars/token.
func (sm *sessionManagerImpl) estimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += utf8.RuneCountInString(m.Content)
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}

// modelForSession returns the model to use for a session.
func (sm *sessionManagerImpl) modelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		if model, ok := sm.al.sessionModels.Load(sessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
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
	if overrideID := sm.al.GetSessionAgent(sessionKey); overrideID != "" {
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

// summarizeSessionWithError summarizes the conversation history for a session and returns any error.
// Passes ALL old messages to the LLM with instructions to create a comprehensive summary.
// Returns statistics about the operation and any error that occurred.
func (sm *sessionManagerImpl) summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	if agent == nil || agent.Provider == nil {
		return nil, fmt.Errorf("no provider available for summarization")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)

	// Need at least 3 messages to summarize (keep last 2 for continuity)
	if len(history) <= 2 {
		return nil, fmt.Errorf("not enough messages to summarize (need at least 3, have %d)", len(history))
	}

	// Calculate before stats
	beforeMessages := len(history)
	beforeTokens := sm.estimateTokens(history)

	// Summarize everything except the last 2 messages (kept for continuity).
	// Note: history contains only user/assistant/tool messages — no system prompt.
	toSummarize := history[:len(history)-2]

	if len(toSummarize) == 0 {
		return nil, fmt.Errorf("no messages available to summarize")
	}

	// Build comprehensive summary prompt with ALL old messages
	prompt := "Please provide a comprehensive summary of the following conversation. " +
		"Capture all important context, decisions, facts, and action items so that " +
		"someone reading just this summary would understand what happened.\n\n"

	if existingSummary != "" {
		prompt += "=== PREVIOUS SUMMARY ===\n" + existingSummary + "\n\n"
	}

	prompt += "=== CONVERSATION TO SUMMARIZE ===\n"
	for _, m := range toSummarize {
		role := strings.ToUpper(m.Role)
		content := m.Content
		// Truncate very long messages for the summary prompt
		if len(content) > 4000 {
			content = content[:4000] + "\n[Content truncated...]"
		}
		prompt += fmt.Sprintf("%s: %s\n\n", role, content)
	}

	prompt += "=== END OF CONVERSATION ===\n\n" +
		"Now provide a detailed summary that preserves all critical context."

	// Call LLM to summarize everything
	resp, err := agent.Provider.Chat(ctx, []providers.Message{{Role: "user", Content: prompt}}, nil, agent.Model, map[string]interface{}{
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
	// Keep only the last 2 messages (the ones not summarized above)
	agent.Sessions.TruncateHistory(sessionKey, 2)
	agent.Sessions.Save(sessionKey)

	// Calculate after stats
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	afterMessages := len(afterHistory)
	afterTokens := sm.estimateTokens(afterHistory)

	return &SummarizeStats{
		BeforeMessages:  beforeMessages,
		AfterMessages:   afterMessages,
		DroppedMessages: beforeMessages - afterMessages,
		BeforeTokens:    beforeTokens,
		AfterTokens:     afterTokens,
		SavedTokens:     beforeTokens - afterTokens,
	}, nil
}
