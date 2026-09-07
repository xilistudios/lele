// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// trackTokenUsage records token usage from an LLM response to the session manager
// If the response includes usage data, it's tracked directly; otherwise, estimates
// are calculated using 2.5 chars/token heuristic (see providers.ResponseTokenCounts,
// the shared accounting used by subagent tool loops too).
func trackTokenUsage(
	sessions *session.SessionManager,
	sessionKey string,
	agentID string,
	messages []providers.Message,
	response *providers.LLMResponse,
) {
	if sessionKey == "" || response == nil || sessions == nil {
		return
	}

	inputTokens, outputTokens := providers.ResponseTokenCounts(messages, response)
	sessions.AddTokenCounts(sessionKey, inputTokens, outputTokens)

	if response.Usage != nil {
		logger.DebugCF("agent", "Token usage tracked", map[string]interface{}{
			"agent_id":           agentID,
			"session_key":        sessionKey,
			"prompt_tokens":      response.Usage.PromptTokens,
			"completion_tokens":  response.Usage.CompletionTokens,
			"total_tokens":       response.Usage.TotalTokens,
			"cache_read_tokens":  response.Usage.CacheReadInputTokens,
			"cache_write_tokens": response.Usage.CacheCreationInputTokens,
		})
	} else {
		logger.DebugCF("agent", "Token usage estimated (provider returned no usage data)", map[string]interface{}{
			"agent_id":    agentID,
			"session_key": sessionKey,
			"input_est":   inputTokens,
			"output_est":  outputTokens,
		})
	}
}

// newSubagentTokenReporter builds the token-usage reporter wired into each
// agent's SubagentManager (see tool_coordinator.go). It receives the owner
// session key resolved by the subagent runner - the spawner's runtime session
// key, falling back to the routing origin key - which is the same key the main
// agent loop tracks under, so subagent spend lands in the parent's cumulative
// counters instead of the subagent's own child session.
//
// Save flushes the parent's metadata immediately after each increment: an
// async subagent typically finishes after the parent's turn-end Save, and its
// contribution must not live only in RAM until some later turn (or a restart)
// silently drops it. Persistence failures are logged and swallowed - losing a
// delta is preferable to killing a running task.
func newSubagentTokenReporter(sessions *session.SessionManager) func(sessionKey string, inputTokens, outputTokens int) {
	return func(sessionKey string, inputTokens, outputTokens int) {
		if sessions == nil || sessionKey == "" {
			return
		}
		sessions.AddTokenCounts(sessionKey, inputTokens, outputTokens)
		if err := sessions.Save(sessionKey); err != nil {
			logger.WarnCF("agent", "Failed to persist subagent token usage", map[string]interface{}{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
	}
}
