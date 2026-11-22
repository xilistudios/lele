package agent

import (
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

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
