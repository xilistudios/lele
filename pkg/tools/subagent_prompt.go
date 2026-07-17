package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

// jsonOutcome is used to unmarshal JSON-formatted subagent output.
// Some LLMs return structured JSON instead of the expected text format.
type jsonOutcome struct {
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	ContextNeeded string `json:"context_needed"`
	Details       string `json:"details"`
}

// extractJSONFromMarkdown extracts JSON content from markdown code blocks.
// It looks for ```json ... ``` blocks first, then ``` ... ``` blocks
// (without language tag) if the inner content starts with "{".
// Returns "" if no JSON block is found.
func extractJSONFromMarkdown(raw string) string {
	// Try ```json ... ``` blocks first (case-insensitive match on "json").
	re := regexp.MustCompile("(?i)```json\\s*\\n([\\s\\S]*?)\\n\\s*```")
	if matches := re.FindStringSubmatch(raw); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Try ``` ... ``` blocks without a language tag. Only return if content
	// starts with '{' to avoid extracting non-JSON code blocks.
	re2 := regexp.MustCompile("```\\s*\\n([\\s\\S]*?)\\n\\s*```")
	if matches := re2.FindStringSubmatch(raw); len(matches) > 1 {
		inner := strings.TrimSpace(matches[1])
		if strings.HasPrefix(inner, "{") {
			return inner
		}
	}

	return ""
}

// stripOuterCodeBlock removes the outermost markdown code block wrapper from
// the raw output if present. LLMs sometimes wrap their entire response in
// ```text ... ``` or plain ``` ... ``` fences. This strips those fences while
// preserving the content inside. If the input has no code block wrapper, it
// is returned unchanged.
func stripOuterCodeBlock(raw string) string {
	trimmed := strings.TrimSpace(raw)

	// Check for opening ``` (possibly with a language tag)
	if !strings.HasPrefix(trimmed, "```") {
		return raw
	}

	// Find the end of the first line (the opening fence).
	firstNewline := strings.Index(trimmed, "\n")
	if firstNewline < 0 {
		return raw
	}
	openFence := trimmed[:firstNewline]
	// The opening fence should be just ``` or ```<lang>, no other content on the line.
	// Verify it's a valid fence line (``` optionally followed by a language tag).
	fenceContent := strings.TrimSpace(strings.TrimPrefix(openFence, "```"))
	if strings.Contains(fenceContent, " ") || strings.Contains(fenceContent, "\t") {
		// Looks like real content starting with ```, not a fence. Return as-is.
		return raw
	}

	// Look for the closing ``` fence at the end.
	afterOpen := trimmed[firstNewline+1:]
	if !strings.HasSuffix(afterOpen, "\n```") && !strings.HasSuffix(afterOpen, "```") {
		return raw
	}

	// Find the last occurrence of ```
	lastFence := strings.LastIndex(afterOpen, "```")
	if lastFence < 0 {
		return raw
	}

	// Ensure the closing fence is on its own line (or is the entire suffix).
	content := afterOpen[:lastFence]
	// Trim trailing newline before the closing fence.
	content = strings.TrimSuffix(content, "\n")

	return content
}

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
		"- For long-running tasks, you may include a PROGRESS: line with a brief status update (e.g., 'PROGRESS: 3 of 5 files analyzed').",
		"- If the task is complete, return STATUS: completed.",
		"- If the task cannot be completed with the current tools/constraints, return STATUS: not_done.",
		"- If you need missing information from the parent agent or user, return STATUS: needs_context.",
		"",
		"Use this exact structure:",
		"STATUS: completed | not_done | needs_context",
		"SUMMARY: one-line summary",
		"PROGRESS: optional brief progress update (omit if not applicable)",
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
	case "completed", "complete", "done", "finished", "success",
		"succeeded", "accomplished", "task_completed", "task_finished",
		"task_succeeded", "task_done":
		return SubagentStatusCompleted
	case "not_done", "notdone", "failed", "failure", "unable",
		"cannot_complete", "task_not_done", "not_completed",
		"blocked", "stuck", "incomplete":
		return SubagentStatusNotDone
	case "needs_context", "need_context", "context_needed",
		"needs_more_context", "needs_more_information", "needs_guidance",
		"waiting", "paused", "need_input", "requires_input":
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

	// --- Step 0: Strip markdown code block wrappers ---
	// LLMs sometimes wrap their entire output in ```text ... ``` or plain ``` ... ```.
	// Strip the outermost code block markers while preserving the content inside.
	cleaned := stripOuterCodeBlock(raw)

	// --- Step 1: Try JSON parsing ---
	// Check for JSON content either in a markdown code block or as a raw JSON object.
	// If JSON parsing succeeds with a valid status, use it and skip text parsing.
	if jsonStr := extractJSONFromMarkdown(cleaned); jsonStr != "" {
		var jo jsonOutcome
		if json.Unmarshal([]byte(jsonStr), &jo) == nil && jo.Status != "" {
			outcome.Status = normalizeSubagentStatus(jo.Status)
			outcome.Summary = strings.TrimSpace(jo.Summary)
			outcome.ContextRequest = strings.TrimSpace(jo.ContextNeeded)
			outcome.Details = strings.TrimSpace(jo.Details)
			if outcome.Details == "" {
				outcome.Details = strings.TrimSpace(raw)
			}
			if outcome.Summary == "" {
				outcome.Summary = summarizeSubagentText(outcome.Details)
			}
			if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
				outcome.ContextRequest = outcome.Summary
			}
			return outcome
		}
	}

	// Also try parsing the cleaned output directly as JSON (without markdown fences).
	trimmedCleaned := strings.TrimSpace(cleaned)
	if strings.HasPrefix(trimmedCleaned, "{") {
		var jo jsonOutcome
		if json.Unmarshal([]byte(trimmedCleaned), &jo) == nil && jo.Status != "" {
			outcome.Status = normalizeSubagentStatus(jo.Status)
			outcome.Summary = strings.TrimSpace(jo.Summary)
			outcome.ContextRequest = strings.TrimSpace(jo.ContextNeeded)
			outcome.Details = strings.TrimSpace(jo.Details)
			if outcome.Details == "" {
				outcome.Details = strings.TrimSpace(raw)
			}
			if outcome.Summary == "" {
				outcome.Summary = summarizeSubagentText(outcome.Details)
			}
			if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
				outcome.ContextRequest = outcome.Summary
			}
			return outcome
		}
	}

	// --- Step 2: Line-by-line text parsing (original logic) ---
	// Parse structured text output looking for STATUS:, SUMMARY:, etc. prefixes.
	parseSource := cleaned
	var detailLines []string
	collectDetails := false

	for _, line := range strings.Split(parseSource, "\n") {
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

	// --- Step 3: Improved fallback heuristics ---
	// If the structured parsing found "completed" but the text contains signals
	// of failure or needing context, override the status. This handles cases
	// where the LLM didn't follow the expected format exactly.
	if outcome.Status == SubagentStatusCompleted {
		lowerRaw := strings.ToLower(raw)
		switch {
		// needs_context patterns: LLM is asking for information or can't proceed
		case strings.Contains(lowerRaw, "needs context"),
			strings.Contains(lowerRaw, "need more context"),
			strings.Contains(lowerRaw, "need more information"),
			strings.Contains(lowerRaw, "need additional context"),
			strings.Contains(lowerRaw, "missing"),
			strings.Contains(lowerRaw, "need more info"),
			strings.Contains(lowerRaw, "require additional"),
			strings.Contains(lowerRaw, "don't have access to"),
			strings.Contains(lowerRaw, "cannot find"),
			strings.Contains(lowerRaw, "unable to locate"),
			strings.Contains(lowerRaw, "please provide"):
			outcome.Status = SubagentStatusNeedsContext
		// not_done patterns: LLM encountered an error or couldn't finish
		case strings.Contains(lowerRaw, "cannot complete"),
			strings.Contains(lowerRaw, "unable to complete"),
			strings.Contains(lowerRaw, "task not done"),
			strings.Contains(lowerRaw, "not completed"),
			strings.Contains(lowerRaw, "failed to"),
			strings.Contains(lowerRaw, "error occurred"),
			strings.Contains(lowerRaw, "was not able to"),
			strings.Contains(lowerRaw, "could not"),
			strings.Contains(lowerRaw, "did not complete"):
			outcome.Status = SubagentStatusNotDone
		default:
			// completed patterns: positive signals that reinforce "completed" status
			// These are already the default, but listed for documentation clarity.
			_ = strings.Contains(lowerRaw, "task completed successfully") ||
				strings.Contains(lowerRaw, "done") ||
				strings.Contains(lowerRaw, "finished successfully") ||
				strings.Contains(lowerRaw, "all done") ||
				strings.Contains(lowerRaw, "task done")
		}
	}

	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		outcome.ContextRequest = outcome.Summary
	}

	return outcome
}
