// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// trackTokenUsage records token usage from an LLM response to the session manager
// If the response includes usage data, it's tracked directly; otherwise, estimates
// are calculated using a 2.5 chars/token heuristic
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

	if response.Usage != nil {
		sessions.AddTokenCounts(sessionKey, response.Usage.PromptTokens, response.Usage.CompletionTokens)
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
		// Provider returned no usage data — estimate using 2.5 chars/token heuristic
		var inputChars int
		for _, msg := range messages {
			inputChars += utf8.RuneCountInString(msg.Content)
		}
		inputEst := inputChars * 2 / 5
		outputEst := utf8.RuneCountInString(response.Content) * 2 / 5
		sessions.AddTokenCounts(sessionKey, inputEst, outputEst)
		logger.DebugCF("agent", "Token usage estimated (provider returned no usage data)", map[string]interface{}{
			"agent_id":    agentID,
			"session_key": sessionKey,
			"input_est":   inputEst,
			"output_est":  outputEst,
		})
	}
}
