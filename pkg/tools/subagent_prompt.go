package tools

import (
	"strings"
)

func buildSubagentSystemPrompt(baseContext, agentID, agentName, agentWorkspace string) string {
	identity := "You are a focused subagent."
	if agentID != "" {
		identity = "You are a focused " + agentID + " subagent."
	}

	contract := strings.Join([]string{
		"## Subagent Contract",
		"- Work independently on the assigned task using available tools.",
		"- Do not send messages to users, Telegram, or any external chat/channel.",
		"- Report your outcome only in the final response using the required format below.",
		"- If the task is complete, return STATUS: completed.",
		"- If the task cannot be completed with the current tools/constraints, return STATUS: not_done.",
		"- If you need missing information from the parent agent or user, return STATUS: needs_context.",
		"",
		"Use this exact structure:",
		"STATUS: completed | not_done | needs_context",
		"SUMMARY: one-line summary",
		"CONTEXT_NEEDED: what is missing (required only for needs_context)",
		"DETAILS:",
		"full details",
	}, "\n")

	if baseContext == "" {
		baseContext = identity
	} else {
		baseContext = baseContext + "\n\n---\n\n## Subagent Identity\n\n" +
			"**Agent Type:** " + agentName + " (" + agentID + ")\n" +
			"**Workspace:** " + agentWorkspace
	}

	return baseContext + "\n\n---\n\n" + contract
}

func normalizeSubagentStatus(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.NewReplacer("-", "_", " ", "_").Replace(normalized)

	switch normalized {
	case "completed", "complete", "done", "finished", "success", "task_completed", "task_finished":
		return SubagentStatusCompleted
	case "not_done", "notdone", "failed", "failure", "unable", "cannot_complete", "task_not_done", "not_completed":
		return SubagentStatusNotDone
	case "needs_context", "need_context", "context_needed", "needs_more_context", "needs_more_information", "needs_guidance":
		return SubagentStatusNeedsContext
	case "cancelled", "canceled":
		return SubagentStatusCancelled
	default:
		return SubagentStatusCompleted
	}
}

// isSubagentTerminalStatus returns true for all statuses where the subagent
// is no longer actively executing and will not transition again.
func isSubagentTerminalStatus(status string) bool {
	switch status {
	case SubagentStatusCompleted,
		SubagentStatusFailed,
		SubagentStatusNotDone,
		SubagentStatusCancelled:
		return true
	default:
		return false
	}
}

func summarizeSubagentText(text string) string {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return trimmed
	}
	return ""
}

func parseSubagentOutcome(raw string) subagentOutcome {
	outcome := subagentOutcome{
		Status:  SubagentStatusCompleted,
		Details: strings.TrimSpace(raw),
	}

	var detailLines []string
	collectDetails := false

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		switch {
		case strings.HasPrefix(lower, "status:"):
			outcome.Status = normalizeSubagentStatus(strings.TrimSpace(trimmed[len("status:"):]))
		case strings.HasPrefix(lower, "summary:"):
			outcome.Summary = strings.TrimSpace(trimmed[len("summary:"):])
		case strings.HasPrefix(lower, "context_needed:"),
			strings.HasPrefix(lower, "context needed:"),
			strings.HasPrefix(lower, "needs_context:"),
			strings.HasPrefix(lower, "needs context:"),
			strings.HasPrefix(lower, "question:"),
			strings.HasPrefix(lower, "request:"):
			if idx := strings.Index(trimmed, ":"); idx >= 0 {
				outcome.ContextRequest = strings.TrimSpace(trimmed[idx+1:])
			}
		case strings.HasPrefix(lower, "details:"):
			collectDetails = true
			if value := strings.TrimSpace(trimmed[len("details:"):]); value != "" {
				detailLines = append(detailLines, value)
			}
		default:
			if collectDetails {
				detailLines = append(detailLines, line)
			}
		}
	}

	if len(detailLines) > 0 {
		outcome.Details = strings.TrimSpace(strings.Join(detailLines, "\n"))
	}

	if outcome.Summary == "" {
		outcome.Summary = summarizeSubagentText(outcome.Details)
	}

	if outcome.Status == SubagentStatusCompleted {
		lowerRaw := strings.ToLower(raw)
		switch {
		case strings.Contains(lowerRaw, "needs context"), strings.Contains(lowerRaw, "need more context"), strings.Contains(lowerRaw, "need more information"), strings.Contains(lowerRaw, "need additional context"):
			outcome.Status = SubagentStatusNeedsContext
		case strings.Contains(lowerRaw, "cannot complete"), strings.Contains(lowerRaw, "unable to complete"), strings.Contains(lowerRaw, "task not done"), strings.Contains(lowerRaw, "not completed"):
			outcome.Status = SubagentStatusNotDone
		}
	}

	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		outcome.ContextRequest = outcome.Summary
	}

	return outcome
}
