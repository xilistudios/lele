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

	// The opening line must be a fence line by the same definition the line
	// parser uses (subagentFenceLine), otherwise the two disagree: the wrapper
	// would survive here while the parser opens a block on it and reads the
	// whole report -- STATUS: declaration included -- as quoted body text,
	// which reports an unfinished run as completed.
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
		// The wrapper was never closed: the model wrapped its answer and the
		// stream was cut, or it simply forgot the marker. The content is still
		// the report, so unwrap it -- but only when no other fence of that run
		// is involved, since otherwise the marker belongs to a code block
		// inside the report and removing it would invert the fence parity.
		return afterOpen
	}

	// A closed wrapper ends with the same run it opened with, and its body holds
	// NO fence of that run: "first fence ... last fence" is only the wrapper
	// when nothing in between is fenced too. A report that starts and ends with
	// a complete code block is not wrapped, and stripping those two markers
	// would invert the fence parity of everything in between -- hiding a
	// declared status behind a block the report never opened.
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

// subagentFenceCount counts the fence lines in text that use the same run
// character as run, so a wrapper strip can tell a balanced body from one whose
// parity it would invert.
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

// subagentStatusNoise is whatever a model can wrap around a declared token
// that carries no meaning for the vocabulary: emoji, brackets, quotes,
// backslashes, markdown. Removing it is what makes "❌ not_done" resolve like
// the bare token. Letters, digits, spaces, hyphens and underscores are the
// only characters a status word is built from, so they are what survives.
var subagentStatusNoise = regexp.MustCompile(`[^A-Za-z0-9 _\-]+`)

// subagentAlternationSeparator is the single source of truth for the
// alternation markers a legend of the vocabulary is written with
// ("completed | not_done", "completed/not_done", "completed or not_done"): it
// both splits the declared value into its parts and, via MatchString, says
// whether the value carries any alternation marker at all. The separators
// themselves are not banned inside a declaration.
//
// The "/" alternative is load-bearing, not decoration: "completed/not_done"
// without it resolves as a declaration of completed instead of a legend (see
// TestSubagentStatusFromToken "two-way legend"), so dropping it silently
// re-opens the printed-template-as-declaration hole this guard closes.
var subagentAlternationSeparator = regexp.MustCompile(`(?i)[|/]|\bor\b`)

// subagentStatusTokenMaxWords bounds how many leading words of a declared
// value are tried against the vocabulary. Two covers the two-word tokens
// ("not done", "needs context"); the third word is headroom for a value that
// starts with a filler word before the token.
const subagentStatusTokenMaxWords = 3

// subagentContractFields are the outcome contract's field prefixes, in the
// exact spelling the subagent system prompt prescribes. The case is
// load-bearing: the uppercase spelling is the contract, so it is recognised
// anywhere in the report (a model that writes a preamble before its header
// must not lose the declaration), while the ordinary-case form ("Status:")
// is prose about the work and is only read inside the header block.
var subagentContractFields = []string{
	"STATUS:",
	"SUMMARY:",
	"PROGRESS:",
	"CONTEXT_NEEDED:",
	"CONTEXT NEEDED:",
	"DETAILS:",
}

// subagentIsContractField reports whether line opens with one of the contract
// field prefixes. It expects an already decoration-stripped line.
func subagentIsContractField(line string) bool {
	for _, prefix := range subagentContractFields {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// subagentContentChar matches a character that carries report content. A line
// made only of emoji or punctuation ("🦞", "---", "**") is a sign-off, not the
// line a closing verdict sits on.
var subagentContentChar = regexp.MustCompile(`[A-Za-z0-9]`)

// subagentIsDecorationLine reports whether line carries no report content: a
// blank line, pure decoration ("---", "**", a lone emoji), or a single-line
// HTML comment ("<!-- end of report -->"), which models append as a sign-off.
// Counting such a line as "the last line of the report" pushes the closing
// status rescue past a genuine closing STATUS: and reports the run completed.
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

// subagentStripStatusPrefix removes a leading "STATUS:" (any case) from a line
// that is being reused as prose, such as a summary that defaulted to the
// report's own status declaration.
func subagentStripStatusPrefix(line string) string {
	if lower := strings.ToLower(line); strings.HasPrefix(lower, "status:") {
		return strings.TrimSpace(subagentStripDecor(line[len("status:"):]))
	}
	return line
}

// subagentStatusDowngrade reports whether moving from current to next lowers
// the outcome. Success is the only status a report can lose: every other
// status is already a non-success, so a change between them is a restatement
// of the same verdict, not a downgrade.
func subagentStatusDowngrade(current, next string) bool {
	return current == SubagentStatusCompleted && next != SubagentStatusCompleted
}

// subagentStatusFromToken maps a declared status value to a known status.
// It is the authoritative entry point: the value is first checked for being a
// printed legend of the vocabulary, then resolved by subagentStatusToken. ok
// is false when the value is a legend or when no leading token is in the
// vocabulary; the caller then leaves the outcome undecided instead of
// silently defaulting to completed.
func subagentStatusFromToken(value string) (string, bool) {
	if subagentIsStatusLegend(value) {
		return "", false
	}
	return subagentStatusToken(value)
}

// subagentIsStatusLegend reports whether value enumerates the status
// vocabulary instead of declaring one status. The subagent system prompt
// itself prints "STATUS: completed | not_done | needs_context", and a model
// echoing that template must not be read as having declared the first
// alternative.
//
// A legend is an enumeration of alternatives that INCLUDES A SUCCESS and
// DISAGREES with it: the only reason to print several statuses side by side is
// to offer a menu, and the menu always contains "completed". So the detector
// needs both conditions -- two or more DIFFERENT statuses across the
// alternation parts, and at least one part resolving to
// SubagentStatusCompleted -- before it strips a value of authority.
//
// An enumeration made only of non-successes is NOT a legend: it is one
// unambiguous non-success declaration ("blocked/pending - the CI token is
// missing", "not_done/needs_context", "failed or needs_context"), and treating
// it as a legend threw the declaration away and let the parser fall through to
// its completed default -- exactly the failed-run-reported-as-success
// direction this whole guard exists to prevent. Keeping the authority of a
// non-success enumeration can only ever report a run as unfinished, which is
// the safe side to be wrong on.
//
// A declaration that merely happens to contain a separator keeps its authority
// too -- "STATUS: in progress (3/8 files done)" and
// "STATUS: not_done - pkg/tools/registry.go unreadable" are declarations of
// not_done (rejecting them silently reported the run as completed). Parts that
// resolve to the SAME status are restatements, not alternatives
// ("blocked/failed" is one declaration of not_done).
func subagentIsStatusLegend(value string) bool {
	// Fast path: without an alternation marker there is nothing to enumerate.
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
		// sawSuccess is updated BEFORE the verdict is taken, so the order of
		// the alternatives does not matter: "not_done | completed" and
		// "completed | not_done" are the same legend.
		if status == SubagentStatusCompleted {
			sawSuccess = true
		}
		if status != first && sawSuccess {
			return true
		}
	}
	return false
}

// subagentStatusToken resolves ONE alternative of a declared value against the
// status vocabulary: no legend detection and no recursion, so
// subagentIsStatusLegend can call it on each alternation part safely. The
// token is the value's leading word(s) -- a trailing clause does not hide a
// real declaration ("not_done (blocked by CI)" resolves to not_done). ok is
// false when no leading token is in the vocabulary.
func subagentStatusToken(value string) (string, bool) {
	// A declared value arrives decorated and punctuated ("**not_done**",
	// "not done.", "`completed`", "❌ not_done"), so the line decoration, the
	// trailing sentence punctuation and any emoji around the token are removed
	// before consulting the vocabulary.
	normalized := strings.Trim(subagentStripDecor(value), " .,:;!\"'`*_")
	normalized = strings.TrimSpace(subagentStatusNoise.ReplaceAllString(normalized, " "))
	normalized = strings.ToLower(normalized)
	if status, ok := subagentStatusWord(normalized); ok {
		return status, true
	}

	// The vocabulary is resolved on the leading words: a trailing clause is
	// prose bolted onto a real declaration ("STATUS: not_done (blocked by
	// CI)", "STATUS: blocked by the CI outage") and must not hide it. At most
	// three words are tried, shortest first: no vocabulary token is a
	// word-prefix of another token that resolves to a different status, so
	// the shortest-first order can never steal one status with another's
	// prefix -- it only decides how quickly an unknown head ("nearly there",
	// "waiting on the CI run") gives up.
	words := strings.Fields(normalized)
	for k := 1; k <= len(words) && k <= subagentStatusTokenMaxWords; k++ {
		if status, ok := subagentStatusWord(strings.Join(words[:k], " ")); ok {
			return status, true
		}
	}
	return "", false
}

// subagentStatusWord looks one whitespace/hyphen-normalised value up in the
// status vocabulary. ok is false for anything outside it.
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
		// The near-miss vocabulary models actually declare: the work is
		// under way, so the run is not a success. Leaving these out made
		// "STATUS: in progress" resolve to nothing and be reported as
		// completed -- the one direction this parser must never get wrong --
		// while the equally unfinished "STATUS: incomplete" was not_done.
		"partial", "partially_done", "partially_complete",
		"in_progress", "wip", "not_started",
		// An ambiguous unfinished value resolves to the TERMINAL side.
		// "pending" also qualifies work that merely awaits a human
		// ("pending review by the user"), which is why it once joined
		// waiting/paused here, but needs_context is not a terminal status:
		// a dependency whose report says STATUS: pending never reaches one,
		// so SubagentManager.checkDependencies would leave every task
		// waiting on it polling forever and CleanupTerminalTasks would never
		// collect it. not_done ends the run, and the nuance travels to the
		// parent in the summary. waiting/paused stay needs_context because
		// they are unambiguous asks for input.
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

// normalizeSubagentStatus maps a declared status token onto one of the
// exported statuses, keeping the historical default (completed) for values
// outside the vocabulary. Callers that must know whether the declaration
// actually resolved use subagentStatusFromToken, which reports it.
func normalizeSubagentStatus(value string) string {
	if status, ok := subagentStatusFromToken(value); ok {
		return status
	}
	return SubagentStatusCompleted
}

// isSubagentTerminalStatus returns true for all statuses where the subagent
// is no longer actively executing and will not transition again.
//
// This is the RETAIN/REAP sense of "terminal" (a task that
// SubagentManager.CleanupTerminalTasks may collect and that satisfies
// checkDependencies for the tasks waiting on it), so needs_context is
// deliberately excluded: the run can still be continued. Do not "fix" the
// disagreement with isSubagentTerminal in wait_for_subagent.go, which answers
// the different question "is the wait over" and therefore includes
// needs_context; making them agree strands every dependent task and leaks the
// task entry.
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
	// Bounds for the undelivered-contract fallback: the first few non-empty
	// lines of the output, and at most this many characters of it. Five
	// lines: a three-line window let a "# title", "Scope:" or "Files read:"
	// preamble swallow the window before any declaration line was seen.
	subagentFallbackMaxLines = 5
	subagentFallbackMaxChars = 400
)

// subagentLineDecor matches the decoration models wrap contract lines and
// summary bullets in: markdown emphasis, headings, list bullets, block
// quotes. Stripping it before parsing is what makes `**STATUS: not_done**`
// and `## STATUS: not_done` behave exactly like a bare `STATUS: not_done`.
var subagentLineDecor = regexp.MustCompile("^[\\s>*_#`+\\-]+|[*_`\\s]+$")

// subagentOrderedListMarker matches an ordered-list marker ("1. ", "2) "),
// which subagentLineDecor deliberately does not cover: a leading digit is
// content in every other context.
var subagentOrderedListMarker = regexp.MustCompile(`^\d+[.)]\s+`)

// subagentFenceStart matches a line that OPENS with a fence run, whatever
// follows it: a block opener ("```go"), inline code ("```go vet ./...```"), or
// a closing marker. Such a line is always body text and never a field line.
var subagentFenceStart = regexp.MustCompile("^[>\\s]*(?:[-*+]\\s+|\\d+[.)]\\s+)?(?:`{3,}|~{3,})")

// subagentFenceLine matches a fence line and captures its RUN (three or more
// backticks, or three or more tildes) and its info string. The info string may
// not contain a fence character: a line such as "```go vet ./...```" is inline
// code, not a fence, and treating it as one would swallow the rest of the
// report. Markdown decoration in front of the run is allowed (blockquote,
// bullet, ordered-list marker), because models nest code blocks in lists.
var subagentFenceLine = regexp.MustCompile("^[>\\s]*(?:[-*+]\\s+|\\d+[.)]\\s+)?(`{3,}|~{3,})([^`~]*)$")

// subagentFenceState is the fence state machine: a run opens a block, and only
// a BARE run of the same character and at least the same length closes it --
// the markdown rule. Anything else that looks like a fence inside a block is
// content (a quoted diff line "+ ```go", a "~~~" divider, a bulleted inline-code
// quote), and letting it toggle would invert the state for the rest of the
// report and hide a genuine closing STATUS: line.
type subagentFenceState struct {
	open bool
	run  string
}

// toggle applies line to the state.
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

// subagentStripDecor returns line without its leading/trailing decoration.
func subagentStripDecor(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = subagentOrderedListMarker.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(subagentLineDecor.ReplaceAllString(trimmed, ""))
}

// Opening-line patterns for output that never declared a status. They are
// anchored at the start of a line on purpose: an engineering report normally
// narrates the problems it already solved, so a whole-document substring scan
// ("missing", "failed to", "could not") reads successful runs as unfinished.
// Only a self-referential opening declaration counts. Compiled once here.
//
// Bare subjects ("blocked", "stuck", "not done", "task ... incomplete") must
// carry a delimiter or terminator to count, because the subject-less form also
// matches report prose about the code the subagent just fixed ("Blocked jobs
// are now retried", "Stuck uploads are now surfaced", "Task incomplete
// handling was added", "Not done items are now visible") and would re-create
// the very bug being fixed. The delimiter is a terminator ("Blocked.") or a
// clause break ("Blocked:") -- never a bare hyphen, which only joins words
// inside prose compounds ("Blocked-and-later-retried", "Blocked-by-design
// queue jobs") -- plus the spaced prepositions that open real failure
// sentences ("Blocked on CI outage.", "Stuck on circular import."). The one
// un-anchored pattern is deliberately narrow
// (first person + inability + complete/finish + the task itself) so a failure
// admitted inside its own opening sentence is still caught. "unable to" and
// "not able to" are their own alternatives because "cannot"/"can't" take no
// "to": a single shared "\s+to\s+" made "I cannot complete", "I'm unable to
// complete" and "I was not able to finish" unreachable.
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
		// "Almost done", "mostly complete", "partially finished": the value a
		// model declares when it is close but not there. It is not a vocabulary
		// token, so it lands here through the STATUS: arm's classifier. Like
		// every other bare subject in this table it must carry a delimiter, or
		// the attributive use -- a successful report about the code it just
		// fixed, "Partially completed orders are now resumed on restart" -- is
		// read as an unfinished run. The delimiter is a terminator, a clause
		// break, a contrast ("but", "except"), a trailing qualifier, or the end
		// of the line; the doubled space is the residue subagentStatusNoise
		// leaves where the punctuation used to be.
		regexp.MustCompile(`(?i)^(almost|mostly|nearly|partially|partly)\s+(done|complete|completed|finished)(\s*$|[.,;:!?–—-]|\s{2,}|\s+(but|though|except|however|only|still|yet|when|once|because|as)\b)`),
		regexp.MustCompile(`(?i)^blocked\s*(?::|[.!?–—]|\s+(?:by|on|at|with)\b)`),
		regexp.MustCompile(`(?i)^(i\s+)?(got\s+)?stuck\s*(?::|[.!?–—]|\s+(?:by|on|at|with)\b)`),
		regexp.MustCompile(`(?i)^(i\s+)?ran\s+out\s+of\s+(iterations|time|budget|turns)\b`),
		regexp.MustCompile(`(?i)\bi\s+(could\s+not|couldn't|cannot|can't|was\s+unable\s+to|was\s+not\s+able\s+to)\s+(complete|finish)\s+(the|this|my)\s+(task|work|job|assignment)\b`),
	}
)

// subagentFallbackOpeningLines returns the head of the output the fallback
// classifier is allowed to look at: the first maxLines non-empty trimmed
// lines, capped at maxChars characters so a long opening paragraph cannot
// smuggle a match in from deep inside the report. Lines arrive decoration
// stripped, which is what makes "**I cannot complete...**" and "- I need more
// context" match the same anchored patterns as their bare forms.
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

// classifySubagentOpening applies the opening-line patterns to output that
// carried no STATUS: declaration and returns the downgrade it implies, or ""
// when the opening says nothing about the outcome. Lines are scanned in order
// and the FIRST line that declares something wins; within one line
// needs_context is checked before not_done, keeping the precedence of the
// status vocabulary for a line that matches both classes.
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

// subagentOutcomeFromJSON builds an outcome out of a JSON-shaped subagent
// response. ok is false when the payload carries no status the vocabulary
// resolves, and the caller then falls back to text parsing. A declared JSON
// status is final against the heuristics; the report's own STATUS: line
// outranks it, because models paste example payloads into prose reports.
func subagentOutcomeFromJSON(jsonStr, raw string) (subagentOutcome, bool) {
	var jo jsonOutcome
	if json.Unmarshal([]byte(jsonStr), &jo) != nil {
		return subagentOutcome{}, false
	}
	// An empty or unresolvable status is NOT a declaration. Locking the
	// outcome to the completed default here would silence a trailing
	// "STATUS: not_done" line forever, so the payload falls back to the text
	// parser below instead.
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

	// explicit records that the subagent declared its own status on a STATUS:
	// line. A JSON payload is a weaker declaration: it is located up front but
	// only used when the text declared nothing (Step 3.5).
	// Declared statuses are authoritative against every heuristic below, and an
	// unrecognised value never sets this flag; the one thing that can still
	// change the outcome afterwards is a LATER declaration by the report itself
	// and only downwards (see the STATUS: arm: an echoed "completed" cannot
	// upgrade a downgraded run, a genuine late "not_done" is honoured).
	explicit := false

	// --- Step 0: Strip markdown code block wrappers ---
	// LLMs sometimes wrap their entire output in ```text ... ``` or plain ``` ... ```.
	// Strip the outermost code block markers while preserving the content inside.
	cleaned := stripOuterCodeBlock(raw)

	// --- Step 1: Locate a JSON payload, without letting it decide yet ---
	// A payload that carries a status field is a declaration, but a WEAKER one
	// than the report's own text: models paste example payloads into the body
	// of an otherwise textual report, and letting such an example decide would
	// silently rewrite the STATUS: line the report opened with -- the exact
	// bug this parser exists to prevent. The payload is therefore only used
	// when the text never declares a status (Step 2.5).
	var jsonOutcome subagentOutcome
	hasJSONOutcome := false
	if jsonStr := extractJSONFromMarkdown(cleaned); jsonStr != "" {
		jsonOutcome, hasJSONOutcome = subagentOutcomeFromJSON(jsonStr, raw)
	}
	if !hasJSONOutcome {
		// Also try parsing the cleaned output directly as JSON (no fences).
		if trimmedCleaned := strings.TrimSpace(cleaned); strings.HasPrefix(trimmedCleaned, "{") {
			jsonOutcome, hasJSONOutcome = subagentOutcomeFromJSON(trimmedCleaned, raw)
		}
	}

	// --- Step 2: Line-by-line text parsing ---
	// Parse structured output looking for STATUS:, SUMMARY:, etc. prefixes.
	// Field lines are only read while the header lasts: it ends at the
	// first prose line, and DETAILS: ends it too (everything after it is
	// report body collected verbatim).
	parseSource := cleaned
	parseLines := strings.Split(parseSource, "\n")
	var detailLines []string
	collectDetails := false
	// lastNonEmptyIdx is the index of the last non-empty line of the whole
	// output, lastDeclaredStatus/lastDeclaredIdx record a STATUS: declaration
	// consumed from the details body. Together they tell a status that CLOSES
	// the report apart from one merely quoted in it (see after the loop).
	// fence tracks fenced code blocks: a STATUS: line inside one is always a
	// quote, never a closing declaration.
	lastNonEmptyIdx, lastDeclaredStatus, lastDeclaredIdx := -1, "", -1
	var fence subagentFenceState
	// headerEnded marks the end of the report header: the run of contract
	// field lines at the very top of the output. The first prose line ends
	// it for good, so a "Status: blocked" that appears later in a prose
	// report is narration about the work, not a declaration.
	headerEnded := false

	for i, line := range parseLines {
		// A line that opens with a fence marker is never a field line: it is
		// a block marker or inline code ("```go vet ./...```"). Only a marker
		// toggles the fenced-block state (and inside a block only a bare run
		// closes it); either way the line is body text (kept verbatim inside
		// DETAILS) and cannot end the header, so a template fence printed
		// before the real header leaves that header intact. subagentStripDecor
		// is deliberately not used here: it eats the backticks that ARE the
		// marker.
		//
		// A fence line is also not content for the purposes of
		// lastNonEmptyIdx: a trailing divider ("~~~", "```") is decoration, and
		// counting it would push "the last line" past a genuine closing
		// STATUS: line and defeat the rescue.
		if subagentFenceStart.MatchString(line) {
			fence.toggle(line)
			if collectDetails {
				detailLines = append(detailLines, line)
			}
			continue
		}

		// A pure decoration line ("---", "**", a lone emoji, an HTML comment)
		// carries no content either: it is not the "last line" a closing
		// verdict sits on, so it must not push lastNonEmptyIdx past a genuine
		// closing STATUS: line.
		if !subagentIsDecorationLine(line) {
			lastNonEmptyIdx = i
		}

		// DETAILS: is a terminal section. Everything after it is report body,
		// even lines that look like fields: a subagent quoting this very
		// contract, or documenting this bug, contains a literal STATUS: line
		// that must not rewrite the outcome already declared above it.
		if collectDetails {
			detailLines = append(detailLines, line)
			// Models quote the contract inside ``` fences, and a fenced block
			// at the end of a report is the most likely place for a literal
			// "STATUS: not_done" that says nothing about the run: inside a
			// fence the line is still appended verbatim to the details, but
			// it never counts as a closing declaration.
			if !fence.open {
				if stripped := subagentStripDecor(line); strings.HasPrefix(strings.ToLower(stripped), "status:") {
					if declared, ok := subagentStatusFromToken(stripped[len("status:"):]); ok {
						lastDeclaredStatus, lastDeclaredIdx = declared, i
					}
				}
			}
			continue
		}

		// A line inside a fence that is not being collected as DETAILS is a
		// quote: never a field line, never able to end the header.
		if fence.open {
			continue
		}

		// Decoration stripped: models render contract lines as
		// "**STATUS: not_done**", "## STATUS: not_done" or "- STATUS: …", and
		// those must behave exactly like the bare form. The RAW line is what
		// goes into the details body, so report text is never altered.
		trimmed := subagentStripDecor(line)
		lower := strings.ToLower(trimmed)
		if trimmed == "" {
			continue
		}

		// Past the header, only a field the contract actually prescribes is
		// still read. Models write a preamble before the contract ("Here is my
		// report:"), and dropping the fields behind it would report a genuine
		// not_done as completed, so the contract's own uppercase spelling is
		// recognised anywhere. Ordinary-case prose that enumerates the task
		// states ("Status: blocked") spells the same words differently and
		// deliberately stays inert: only the contract's spelling is a field.
		if headerEnded && !subagentIsContractField(trimmed) {
			continue
		}

		switch {
		case strings.HasPrefix(lower, "status:"):
			status, declared := subagentStatusFromToken(trimmed[len("status:"):])
			if !declared {
				// A value outside the vocabulary ("STATUS: could not complete
				// the task", "STATUS: almost done but one test is red") is
				// still an attempt at a declaration, so it is read with the
				// same opening-line classifier the prose fallback uses; a
				// value that matches nothing leaves the outcome undecided.
				// The value gets the same noise pass the token path applies,
				// so "❌ could not complete" and `"could not complete"`
				// classify alike -- but an INFERRED status never claims the
				// authority of a declared one: it only fills an outcome that
				// no declaration decided.
				if explicit {
					continue
				}
				normalized := strings.TrimSpace(subagentStatusNoise.ReplaceAllString(subagentStripDecor(trimmed[len("status:"):]), " "))
				if inferred := classifySubagentOpening([]string{normalized}); inferred != "" {
					outcome.Status = inferred
				}
				continue
			}
			// A later declaration can only lower the outcome. An upgrade -- or
			// a change between two non-success statuses -- is the echo of the
			// contract (a model restating the vocabulary, a quoted example)
			// and must not rewrite the verdict the report already declared,
			// while a genuine late downgrade ("STATUS: not_done, the last test
			// still fails") is the subagent's last word and is honoured. This
			// is the one direction the parser must never get wrong: a run that
			// did not finish may not be reported as completed.
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
			// PROGRESS is a contract field that may interleave the header;
			// it carries no outcome data and must not end the header.
		default:
			// The first line that is not a contract field ends the header:
			// whatever comes after it is report prose, field-shaped or not.
			headerEnded = true
		}
	}

	// A STATUS: line that closes the report is a declaration, not body text:
	// models that revise their verdict at the end put it on the last line, and
	// ignoring it would report "completed" over an explicit "not_done" -- the
	// dangerous direction. The rule: a status quoted inside the body does not
	// count, a status that closes the report does -- unless the header already
	// declared one. A header declaration outranks the closing line, so a
	// trailing "STATUS: ..." (a quoted contract example, a doc snippet) cannot
	// rewrite the verdict the report opened with; the closing rule exists only
	// to rescue output whose header never declared a status at all. The price is
	// deliberate and worth stating: a report that declares a status in its
	// header and only revises it in the closing line (after DETAILS:, where
	// reports naturally end) is taken at its header word -- a header-internal
	// revision is still honoured by the downgrade rule, the closing one is not.
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

	// --- Step 3: Fallback for output that never declared a status ---
	// Invariant: an explicit declaration is authoritative against the
	// heuristics. The contract asks for a STATUS: line, and the upstream guards
	// in toolloop.go emit one themselves when a run is genuinely unfinished, so
	// a declared status is returned as written and no heuristic may rewrite it
	// (the only writer left is another DECLARATION in the same report, and only
	// downwards).
	//
	// The fallback only classifies an undelivered contract, and it does so by
	// its opening line. A former whole-document substring scan treated words
	// such as "missing", "could not" or "failed to" anywhere in the report as
	// proof of failure, which misread successful runs: engineers normally
	// describe the errors they fixed. Anything the opening does not state
	// about itself is left at the completed default.
	//
	// The narrowing is deliberate: only the opening window (the first
	// subagentFallbackMaxLines non-empty lines, at most subagentFallbackMaxChars
	// characters) and a closing STATUS: line are classified. A failure declared
	// only in the middle of a prose report is reported as completed, and that
	// trade is accepted because the contract -- plus the two synthetic guards in
	// toolloop.go -- always declares a status, so the mid-report case is output
	// that broke the contract in a way no anchored rule can tell apart from
	// narration of problems already solved.
	if !explicit && outcome.Status == SubagentStatusCompleted {
		opening := subagentFallbackOpeningLines(parseSource, subagentFallbackMaxLines, subagentFallbackMaxChars)
		if status := classifySubagentOpening(opening); status != "" {
			outcome.Status = status
		}
	}

	// --- Step 4: A JSON payload fills in what the text did not decide ---
	// It carries its own summary and details, so the whole outcome is taken
	// from it rather than merged. Only an outcome the text left at the
	// completed default is open to it: once the text or its opening line says
	// the run did not finish, a pasted payload cannot turn that into success,
	// and once the text says it finished, a pasted failure example cannot
	// downgrade it either.
	if hasJSONOutcome && !explicit && outcome.Status == SubagentStatusCompleted {
		return jsonOutcome
	}

	if outcome.Status == SubagentStatusNeedsContext && outcome.ContextRequest == "" {
		// The parent reads the request as prose, so a summary that is only a
		// markdown bullet must not carry the bullet over. A report that declared
		// "STATUS: I need more context about the deploy target" has no SUMMARY
		// line and its summary defaults to that declaration, where
		// subagentStripStatusPrefix already removed the field prefix; otherwise
		// the parent would render "Context needed: STATUS: I need more context".
		outcome.ContextRequest = subagentStripDecor(outcome.Summary)
	}

	return outcome
}
