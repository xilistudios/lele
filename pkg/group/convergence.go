package group

import "strings"

func StopReason(state *GroupState) (stop bool, reason string) {
	if state.MaxTurns > 0 && len(state.Transcript) >= state.MaxTurns {
		return true, "max_turns"
	}

	if state.TotalTokenBudget > 0 && state.TotalTokens >= state.TotalTokenBudget {
		return true, "token_budget"
	}

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

func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
