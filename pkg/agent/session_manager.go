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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history := agent.Sessions.GetHistory(sessionKey)
	existingSummary := agent.Sessions.GetSummary(sessionKey)

	// Need at least system prompt + 3 messages to summarize (keep last 2 exchanges)
	if len(history) <= 3 {
		return nil
	}

	// Calculate before stats
	beforeMessages := len(history)
	beforeTokens := sm.estimateTokens(history)

	// Keep system prompt [0] and last 2 messages for continuity
	toSummarize := history[1 : len(history)-2] // Everything between system and last 2

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
	agent.Sessions.TruncateHistory(sessionKey, 4)
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
// It drops the oldest 50% of messages (keeping system prompt and last user message).
func (sm *sessionManagerImpl) forceCompression(agent *AgentInstance, sessionKey string) {
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return
	}

	// Keep system prompt (usually [0]) and the very last message (user's trigger)
	// We want to drop the oldest half of the *conversation*
	// Assuming [0] is system, [1:] is conversation
	conversation := history[1 : len(history)-1]
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

	newHistory := make([]providers.Message, 0)
	newHistory = append(newHistory, history[0]) // System prompt

	// Add a note about compression
	compressionNote := fmt.Sprintf("[System: Emergency compression dropped %d oldest messages due to context limit]", droppedCount)
	// If there was an existing summary, we might lose it if it was in the dropped part (which is just messages).
	// The summary is stored separately in session.Summary, so it persists!
	// We just need to ensure the user knows there's a gap.

	// We only modify the messages list here
	newHistory = append(newHistory, providers.Message{
		Role:    "system",
		Content: compressionNote,
	})

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
