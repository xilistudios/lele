package group

import "strings"

// StopReason evaluates whether the group should stop based on hard limits
// and convergence conditions. It checks conditions in priority order:
//  1. MaxTurns exceeded
//  2. TotalTokenBudget exceeded
//  3. StopKeywords found in last turn
//  4. Repeated content in last 3 turns (converged_repetition)
//
// Returns true and the reason string if the group should stop, or false, ""
// if it should continue.
func StopReason(state *GroupState) (stop bool, reason string) {
	// 1. MaxTurns hard cap
	if state.MaxTurns > 0 && len(state.Transcript) >= state.MaxTurns {
		return true, "max_turns"
	}

	// 2. Token budget
	if state.TotalTokenBudget > 0 && state.TotalTokens >= state.TotalTokenBudget {
		return true, "token_budget"
	}

	// 3. Stop keywords in last turn content (case-insensitive)
	if len(state.StopKeywords) > 0 {
		last, ok := state.LastTurn()
		if ok {
			lower := strings.ToLower(last.Content)
			for _, kw := range state.StopKeywords {
				if strings.Contains(lower, strings.ToLower(kw)) {
					return true, "stop_keyword:" + kw
				}
			}
		}
	}

	// 4. Converged repetition: last 3 turns have identical normalized content.
	// Normalized = lowercase + trimmed whitespace.
	if len(state.Transcript) >= 3 {
		n := len(state.Transcript)
		c0 := normalize(state.Transcript[n-3].Content)
		c1 := normalize(state.Transcript[n-2].Content)
		c2 := normalize(state.Transcript[n-1].Content)
		if c0 != "" && c0 == c1 && c1 == c2 {
			return true, "converged_repetition"
		}
	}

	return false, ""
}

// normalize returns a lowercased, trimmed version of s for comparison.
func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
