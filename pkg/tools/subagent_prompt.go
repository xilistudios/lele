package tools

import (
	"encoding/json"
	"regexp"
	"strings"
)

type jsonOutcome struct {
	Status        string `json:"status"`
	Summary       string `json:"summary"`
	ContextNeeded string `json:"context_needed"`
	Details       string `json:"details"`
}

func extractJSONFromMarkdown(raw string) string {
	re := regexp.MustCompile("(?i)```json\\s*\\n([\\s\\S]*?)\\n\\s*```")
	if matches := re.FindStringSubmatch(raw); len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	re2 := regexp.MustCompile("```\\s*\\n([\\s\\S]*?)\\n\\s*```")
	if matches := re2.FindStringSubmatch(raw); len(matches) > 1 {
		inner := strings.TrimSpace(matches[1])
		if strings.HasPrefix(inner, "{") {
			return inner
		}
	}

	return ""
}

func stripOuterCodeBlock(raw string) string {
	trimmed := strings.TrimSpace(raw)

	firstNewline := strings.Index(trimmed, "\n")
	if firstNewline < 0 {
		return raw
	}
	m := subagentFenceLine.FindStringSubmatch(strings.TrimSpace(trimmed[:firstNewline]))
	if m == nil {
		return raw
	}
	fenceRun := m[1]
	afterOpen := trimmed[firstNewline+1:]

	if subagentFenceCount(afterOpen, fenceRun) == 0 {
		return afterOpen
	}

	if !strings.HasSuffix(strings.TrimSpace(afterOpen), fenceRun) {
		return raw
	}
	lastFence := strings.LastIndex(afterOpen, fenceRun)
	if lastFence < 0 {
		return raw
	}
	content := afterOpen[:lastFence]
	if subagentFenceCount(content, fenceRun) != 0 {
		return raw
	}
	return strings.TrimSuffix(content, "\n")
}

func subagentFenceCount(text, run string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		m := subagentFenceLine.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil && m[1][0] == run[0] {
			count++
		}
	}
	return count
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
		"- The STATUS: line is MANDATORY and must be the FIRST line of your final response.",
		"- For long-running tasks, you may include a PROGRESS: line with a brief status update (e.g., 'PROGRESS: 3 of 5 files analyzed').",
		"- If the task is complete, return STATUS: completed. This is the expected outcome for work that is finished.",
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

var subagentStatusNoise = regexp.MustCompile(`[^A-Za-z0-9 _\-]+`)

var subagentAlternationSeparator = regexp.MustCompile(`(?i)[|/]|\bor\b`)

const subagentStatusTokenMaxWords = 3

var subagentContractFields = []string{
	"STATUS:",
	"SUMMARY:",
	"PROGRESS:",
	"CONTEXT_NEEDED:",
	"CONTEXT NEEDED:",
	"DETAILS:",
}

func subagentIsContractField(line string) bool {
	for _, prefix := range subagentContractFields {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

var subagentContentChar = regexp.MustCompile(`[A-Za-z0-9]`)

func subagentIsDecorationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	stripped := subagentStripDecor(line)
	if stripped == "" || !subagentContentChar.MatchString(stripped) {
		return true
	}
	return strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->")
}

func subagentStripStatusPrefix(line string) string {
	if lower := strings.ToLower(line); strings.HasPrefix(lower, "status:") {
		return strings.TrimSpace(subagentStripDecor(line[len("status:"):]))
	}
	return line
}

func subagentStatusDowngrade(current, next string) bool {
	return current == SubagentStatusCompleted && next != SubagentStatusCompleted
}

func subagentStatusFromToken(value string) (string, bool) {
	if subagentIsStatusLegend(value) {
		return "", false
	}
	return subagentStatusToken(value)
}

func subagentIsStatusLegend(value string) bool {
	if !subagentAlternationSeparator.MatchString(value) {
		return false
	}

	var first string
	sawSuccess := false
	for _, part := range subagentAlternationSeparator.Split(value, -1) {
		status, ok := subagentStatusToken(part)
		if !ok {
			continue
		}
		if first == "" {
			first = status
		}
		if status == SubagentStatusCompleted {
			sawSuccess = true
		}
		if status != first && sawSuccess {
			return true
		}
	}
	return false
}

func subagentStatusToken(value string) (string, bool) {
	normalized := strings.Trim(subagentStripDecor(value), " .,:;!\"'`*_")
	normalized = strings.TrimSpace(subagentStatusNoise.ReplaceAllString(normalized, " "))
	normalized = strings.ToLower(normalized)
	if status, ok := subagentStatusWord(normalized); ok {
		return status, true
	}

	words := strings.Fields(normalized)
	for k := 1; k <= len(words) && k <= subagentStatusTokenMaxWords; k++ {
		if status, ok := subagentStatusWord(strings.Join(words[:k], " ")); ok {
			return status, true
		}
	}
	return "", false
}

func subagentStatusWord(value string) (string, bool) {
	switch strings.NewReplacer("-", "_", " ", "_").Replace(strings.TrimSpace(value)) {
	case "completed", "complete", "done", "finished", "success",
		"succeeded", "accomplished", "task_completed", "task_finished",
		"task_succeeded", "task_done":
		return SubagentStatusCompleted, true
	case "not_done", "notdone", "failed", "failure", "unable",
		"cannot_complete", "task_not_done", "not_completed", "not_complete",
		"not_finished", "not_yet_done",
		"blocked", "stuck", "incomplete",
		"partial", "partially_done", "partially_complete",
		"in_progress", "wip", "not_started",
		"pending":
		return SubagentStatusNotDone, true
	case "needs_context", "need_context", "context_needed",
		"needs_more_context", "needs_more_information", "needs_guidance",
		"waiting", "paused", "need_input", "requires_input":
		return SubagentStatusNeedsContext, true
	case "cancelled", "canceled":
		return SubagentStatusCancelled, true
	default:
		return "", false
	}
}

func normalizeSubagentStatus(value string) string {
	if status, ok := subagentStatusFromToken(value); ok {
		return status
	}
	return SubagentStatusCompleted
}

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

const (
	subagentFallbackMaxLines = 5
	subagentFallbackMaxChars = 400
)

var subagentLineDecor = regexp.MustCompile("^[\\s>*_#`+\\-]+|[*_`\\s]+$")

var subagentOrderedListMarker = regexp.MustCompile(`^\d+[.)]\s+`)

var subagentFenceStart = regexp.MustCompile("^[>\\s]*(?:[-*+]\\s+|\\d+[.)]\\s+)?(?:`{3,}|~{3,})")

var subagentFenceLine = regexp.MustCompile("^[>\\s]*(?:[-*+]\\s+|\\d+[.)]\\s+)?(`{3,}|~{3,})([^`~]*)$")

type subagentFenceState struct {
	open bool
	run  string
}

func (f *subagentFenceState) toggle(line string) {
	m := subagentFenceLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return
	}
	run, info := m[1], strings.TrimSpace(m[2])
	switch {
	case !f.open:
		f.open, f.run = true, run
	case info == "" && run[0] == f.run[0] && len(run) >= len(f.run):
		f.open, f.run = false, ""
	}
}

func subagentStripDecor(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = subagentOrderedListMarker.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(subagentLineDecor.ReplaceAllString(trimmed, ""))
}

var (
	subagentNeedsContextOpenings = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(i\s+|i'?m\s+|im\s+)(need|require)s?\s+(more\s+|additional\s+)?(context|information|info|details|clarification)\b`),
		regexp.MustCompile(`(?i)^(need|require)s?\s+(more\s+|additional\s+)?(context|information|info|details|clarification)(\s*[:?.,;!]|\s+(about|on|for|to|before|with)\b|$)`),
		regexp.MustCompile(`(?i)^needs?_context\s*[:\-–—]`),
		regexp.MustCompile(`(?i)^needs?_context$`),
		regexp.MustCompile(`(?i)^(i\s+)?(cannot|can't|can\s+not|unable\s+to|could\s+not|couldn't)\s+proceed\s+without\b`),
	}

	subagentNotDoneOpenings = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(i\s+|i'?m\s+|im\s+)?(was\s+|am\s+)?(unable\s+to|not\s+able\s+to|cannot|can't|can\s+not|could\s+not|couldn't)\s+(complete|finish)\b`),
		regexp.MustCompile(`(?i)^(i\s+)?(did\s+not|didn't|could\s+not|couldn't)\s+(complete|finish)\b`),
		regexp.MustCompile(`(?i)^task\s+(is\s+)?(not\s+complete|incomplete)\s*[.:!]`),
		regexp.MustCompile(`(?i)^not\s+done\s*[.:!]`),
		regexp.MustCompile(`(?i)^(almost|mostly|nearly|partially|partly)\s+(done|complete|completed|finished)(\s*$|[.,;:!?–—-]|\s{2,}|\s+(but|though|except|however|only|still|yet|when|once|because|as)\b)`),
		regexp.MustCompile(`(?i)^blocked\s*(?::|[.!?–—]|\s+(?:by|on|at|with)\b)`),
		regexp.MustCompile(`(?i)^(i\s+)?(got\s+)?stuck\s*(?::|[.!?–—]|\s+(?:by|on|at|with)\b)`),
		regexp.MustCompile(`(?i)^(i\s+)?ran\s+out\s+of\s+(iterations|time|budget|turns)\b`),
		regexp.MustCompile(`(?i)\bi\s+(could\s+not|couldn't|cannot|can't|was\s+unable\s+to|was\s+not\s+able\s+to)\s+(complete|finish)\s+(the|this|my)\s+(task|work|job|assignment)\b`),
	}
)

func subagentFallbackOpeningLines(text string, maxLines, maxChars int) []string {
	var opening []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := subagentStripDecor(line)
		if trimmed == "" {
			continue
		}
		opening = append(opening, trimmed)
		if len(opening) >= maxLines {
			break
		}
	}

	head := strings.Join(opening, "\n")
	if runes := []rune(head); len(runes) > maxChars {
		head = string(runes[:maxChars])
	}
	return strings.Split(head, "\n")
}

func classifySubagentOpening(lines []string) string {
	for _, line := range lines {
		for _, re := range subagentNeedsContextOpenings {
			if re.MatchString(line) {
				return SubagentStatusNeedsContext
			}
		}
		for _, re := range subagentNotDoneOpenings {
			if re.MatchString(line) {
				return SubagentStatusNotDone
			}
		}
	}
	return ""
}

func subagentOutcomeFromJSON(jsonStr, raw string) (subagentOutcome, bool) {
	var jo jsonOutcome
	if json.Unmarshal([]byte(jsonStr), &jo) != nil {
		return subagentOutcome{}, false
	}
	status, ok := subagentStatusFromToken(jo.Status)
	if !ok {
		return subagentOutcome{}, false
	}

	outcome := subagentOutcome{
		Status:         status,
		Summary:        strings.TrimSpace(jo.Summary),
		ContextRequest: strings.TrimSpace(jo.ContextNeeded),
		Details:        strings.TrimSpace(jo.Details),
	}
	if outcome.Details == "" {
		outcome.Details = strings.TrimSpace(raw)
	}
	if outcome.Summary == "" {
		outcome.Summary = subagentStripStatusPrefix(subagentStripDecor(summarizeSubagentText(outcome.Details)))
	}
	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		outcome.ContextRequest = outcome.Summary
	}
	return outcome, true
}

func parseSubagentOutcome(raw string) subagentOutcome {
	outcome := subagentOutcome{
		Status:  SubagentStatusCompleted,
		Details: strings.TrimSpace(raw),
	}

	explicit := false

	cleaned := stripOuterCodeBlock(raw)

	var jsonOutcome subagentOutcome
	hasJSONOutcome := false
	if jsonStr := extractJSONFromMarkdown(cleaned); jsonStr != "" {
		jsonOutcome, hasJSONOutcome = subagentOutcomeFromJSON(jsonStr, raw)
	}
	if !hasJSONOutcome {
		if trimmedCleaned := strings.TrimSpace(cleaned); strings.HasPrefix(trimmedCleaned, "{") {
			jsonOutcome, hasJSONOutcome = subagentOutcomeFromJSON(trimmedCleaned, raw)
		}
	}

	parseSource := cleaned
	parseLines := strings.Split(parseSource, "\n")
	var detailLines []string
	collectDetails := false
	lastNonEmptyIdx, lastDeclaredStatus, lastDeclaredIdx := -1, "", -1
	var fence subagentFenceState
	headerEnded := false

	for i, line := range parseLines {
		if subagentFenceStart.MatchString(line) {
			fence.toggle(line)
			if collectDetails {
				detailLines = append(detailLines, line)
			}
			continue
		}

		if !subagentIsDecorationLine(line) {
			lastNonEmptyIdx = i
		}

		if collectDetails {
			detailLines = append(detailLines, line)
			if !fence.open {
				if stripped := subagentStripDecor(line); strings.HasPrefix(strings.ToLower(stripped), "status:") {
					if declared, ok := subagentStatusFromToken(stripped[len("status:"):]); ok {
						lastDeclaredStatus, lastDeclaredIdx = declared, i
					}
				}
			}
			continue
		}

		if fence.open {
			continue
		}

		trimmed := subagentStripDecor(line)
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}

		if headerEnded && !subagentIsContractField(trimmed) {
			continue
		}

		switch {
		case strings.HasPrefix(lower, "status:"):
			status, declared := subagentStatusFromToken(trimmed[len("status:"):])
			if !declared {
				if explicit {
					continue
				}
				normalized := strings.TrimSpace(subagentStatusNoise.ReplaceAllString(subagentStripDecor(trimmed[len("status:"):]), " "))
				if inferred := classifySubagentOpening([]string{normalized}); inferred != "" {
					outcome.Status = inferred
				}
				continue
			}
			if explicit && !subagentStatusDowngrade(outcome.Status, status) {
				continue
			}
			outcome.Status = status
			explicit = true
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
		case strings.HasPrefix(lower, "progress:"):
		default:
			headerEnded = true
		}
	}

	if !explicit && lastDeclaredStatus != "" && lastDeclaredIdx == lastNonEmptyIdx {
		outcome.Status = lastDeclaredStatus
		explicit = true
	}

	if len(detailLines) > 0 {
		outcome.Details = strings.TrimSpace(strings.Join(detailLines, "\n"))
	}

	if outcome.Summary == "" {
		outcome.Summary = subagentStripStatusPrefix(subagentStripDecor(summarizeSubagentText(outcome.Details)))
	}

	if !explicit && outcome.Status == SubagentStatusCompleted {
		opening := subagentFallbackOpeningLines(parseSource, subagentFallbackMaxLines, subagentFallbackMaxChars)
		if status := classifySubagentOpening(opening); status != "" {
			outcome.Status = status
		}
	}

	if hasJSONOutcome && !explicit && outcome.Status == SubagentStatusCompleted {
		return jsonOutcome
	}

	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		outcome.ContextRequest = subagentStripDecor(outcome.Summary)
	}

	return outcome
}
