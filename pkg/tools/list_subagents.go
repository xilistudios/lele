package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ListSubagentsTool lists a session's subagent tasks from the durable ledger
// (SubagentManager.LedgerEntries): live tasks merged with the tasks recovered
// from their persisted sessions.
//
// The ledger view is what makes finished work survive context compaction: the
// tool result of a `spawn` call is the first thing a summarizer drops, but the
// task itself stays in the ledger (in memory while it is alive, on disk after
// retention reaped it, and across restarts). The tool is therefore paginated —
// a long-running session accumulates hundreds of tasks, and dumping all of them
// into one tool result would cost more context than the compaction it is meant
// to survive.
type ListSubagentsTool struct {
	manager *SubagentManager
}

func NewListSubagentsTool(manager *SubagentManager) *ListSubagentsTool {
	return &ListSubagentsTool{manager: manager}
}

func (t *ListSubagentsTool) Name() string {
	return "list_active_subagents"
}

// Description returns a short summary; it is echoed verbatim into the system
// prompt by ToolRegistry.GetSummaries, so it must stay compact. The full status
// enum lives in Parameters, not here.
func (t *ListSubagentsTool) Description() string {
	return "List this session's subagent tasks, newest first, paginated with limit/offset. The ledger is complete and survives context compaction and restarts: finished tasks stay listed after their tool results are gone. Default status=active; use status=all for history. Use wait_for_subagent for the full result of a live task."
}

// statusFilterActive is the default status filter: the tasks a caller normally
// wants to see, i.e. everything that can still produce a result.
const statusFilterActive = "active"

// statusFilterAll disables status filtering.
const statusFilterAll = "all"

// Pagination defaults and bounds. The limit is clamped rather than rejected: a
// model that asks for 1000 rows wants "as many as possible", not an error.
const (
	defaultSubagentListLimit = 20
	maxSubagentListLimit     = 100
)

func (t *ListSubagentsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"status": map[string]interface{}{
				"type": "string",
				"enum": []string{
					statusFilterActive,
					statusFilterAll,
					SubagentStatusRunning,
					SubagentStatusPending,
					SubagentStatusNeedsContext,
					SubagentStatusCompleted,
					SubagentStatusFailed,
					SubagentStatusCancelled,
					SubagentStatusNotDone,
					SubagentStatusUnknown,
				},
				"description": "Filter by status before pagination (default: active = running, pending or needs_context). Use 'all' for the full history, 'unknown' for rows whose outcome was never recorded (interrupted or re-pinned tasks).",
				"default":     statusFilterActive,
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": fmt.Sprintf("Maximum tasks per page (default: %d, max: %d)", defaultSubagentListLimit, maxSubagentListLimit),
				"default":     defaultSubagentListLimit,
				"minimum":     1,
				"maximum":     maxSubagentListLimit,
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Number of tasks to skip, for paging (default: 0)",
				"default":     0,
				"minimum":     0,
			},
			"include_completed": map[string]interface{}{
				"type":        "boolean",
				"description": "Deprecated alias for status=all: if true and status is omitted, finished tasks are included too (default: false)",
				"default":     false,
			},
		},
	}
}

// listSubagentsQuery is the parsed, clamped argument set of one call. Parsing
// is separated from rendering so the clamping rules are testable on their own.
type listSubagentsQuery struct {
	status   string // one of the enum values, already validated
	limit    int
	offset   int
	explicit bool // status was given explicitly (it wins over include_completed)
}

func (t *ListSubagentsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if t.manager == nil {
		return ErrorResult("Subagent manager not configured")
	}

	query, err := parseListSubagentsArgs(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	scopeKey := subagentScopeKeyFromCtx(ctx)

	// ONE snapshot of the task map, shared by the hidden-by-scope count and by
	// the ledger merge. Taking it twice (once here, once inside LedgerEntries)
	// lets a concurrent reap make the count disagree with the rows it describes.
	// The count is over memory tasks only: the disk side is already
	// prefix-scoped, so it can only return rows of this session and must not
	// contribute.
	memoryTasks := t.manager.ListTasks()

	// The ledger is the source of truth: memory ∪ disk, deduplicated by session
	// key, newest first. Scoping is applied inside it with the same
	// three-identity relation the rest of the codebase uses
	// (taskBelongsToSession).
	var scopeKeys []string
	if scopeKey != "" {
		scopeKeys = []string{scopeKey}
	}
	entries := t.manager.ledgerEntriesFrom(memoryTasks, scopeKeys)

	hiddenByScope := 0
	if scopeKey != "" {
		for _, task := range memoryTasks {
			if task != nil && !ledgerScopeMatches(task, scopeKeys) {
				hiddenByScope++
			}
		}
	}

	// Filtering happens BEFORE pagination: `total` is the size of the filtered
	// set, so page N of M is meaningful and the footer can report how much is
	// left.
	filtered := filterLedgerByStatus(entries, query.status)

	if len(filtered) == 0 {
		// Nothing to show. When the ledger itself is empty the historical
		// "No subagent tasks found." is the honest answer; the status-specific
		// messages below only make sense when tasks exist but were filtered out.
		if len(entries) == 0 {
			return SilentResult(emptyLedgerMessage(query, scopeKey != "", hiddenByScope))
		}
		return SilentResult(emptyListMessage(query, scopeKey != "", hiddenByScope))
	}

	page, start, end := paginateLedger(filtered, query.limit, query.offset)
	t.fillPageDetails(page)

	return SilentResult(renderSubagentPage(query, filtered, page, start, end))
}

// parseListSubagentsArgs validates and clamps the tool arguments.
//
// JSON numbers arrive as float64, so limit/offset are read as such; a wrong
// type falls back to the default instead of failing the call. An unknown status
// is an error rather than a silent "all": a typo that quietly widens the filter
// would hand the model a different answer than it asked for.
func parseListSubagentsArgs(args map[string]interface{}) (listSubagentsQuery, error) {
	query := listSubagentsQuery{
		status: statusFilterActive,
		limit:  defaultSubagentListLimit,
		offset: 0,
	}

	if raw, ok := args["status"].(string); ok && raw != "" {
		status := strings.ToLower(strings.TrimSpace(raw))
		if !isValidSubagentStatusFilter(status) {
			return query, fmt.Errorf("invalid status %q; valid values: %s",
				raw, strings.Join(subagentStatusFilterValues(), ", "))
		}
		query.status = status
		query.explicit = true
	}

	// Back-compat: include_completed=true used to mean "show everything". It
	// keeps that meaning only while status is absent — an explicit status is a
	// more precise request and always wins.
	if !query.explicit {
		if includeCompleted, ok := args["include_completed"].(bool); ok && includeCompleted {
			query.status = statusFilterAll
		}
	}

	if raw, ok := args["limit"].(float64); ok {
		query.limit = clampSubagentListLimit(int(raw))
	}
	if raw, ok := args["offset"].(float64); ok && raw > 0 {
		query.offset = int(raw)
	}

	return query, nil
}

// clampSubagentListLimit bounds a requested page size. A non-positive request
// means "use the default"; anything above the maximum is capped so one call
// cannot flood the context.
func clampSubagentListLimit(limit int) int {
	if limit <= 0 {
		return defaultSubagentListLimit
	}
	if limit > maxSubagentListLimit {
		return maxSubagentListLimit
	}
	return limit
}

// subagentStatusFilterValues lists the accepted status filters in the order the
// error message and the schema present them.
func subagentStatusFilterValues() []string {
	return []string{
		statusFilterActive,
		statusFilterAll,
		SubagentStatusRunning,
		SubagentStatusPending,
		SubagentStatusNeedsContext,
		SubagentStatusCompleted,
		SubagentStatusFailed,
		SubagentStatusCancelled,
		SubagentStatusNotDone,
		SubagentStatusUnknown,
	}
}

// isValidSubagentStatusFilter reports whether status is one of the accepted
// filter values.
func isValidSubagentStatusFilter(status string) bool {
	for _, valid := range subagentStatusFilterValues() {
		if status == valid {
			return true
		}
	}
	return false
}

// isActiveSubagentStatus reports whether a task can still produce a result.
// pending is included because a task waiting on dependencies has not finished,
// and needs_context because it is blocked on the caller.
func isActiveSubagentStatus(status string) bool {
	switch status {
	case SubagentStatusRunning, SubagentStatusPending, SubagentStatusNeedsContext:
		return true
	default:
		return false
	}
}

// filterLedgerByStatus applies the status filter to the whole ledger. It runs
// before pagination so that `total`, the page window and the footer all
// describe the same filtered set.
func filterLedgerByStatus(entries []SubagentLedgerEntry, status string) []SubagentLedgerEntry {
	if status == statusFilterAll {
		return entries
	}
	filtered := make([]SubagentLedgerEntry, 0, len(entries))
	for _, entry := range entries {
		if status == statusFilterActive {
			if isActiveSubagentStatus(entry.Status) {
				filtered = append(filtered, entry)
			}
			continue
		}
		if entry.Status == status {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// paginateLedger returns the requested window of an already filtered ledger,
// together with its 1-based start and end offsets. An offset past the end
// yields an empty page rather than an error: that is how a caller discovers it
// has paged to the end.
func paginateLedger(entries []SubagentLedgerEntry, limit, offset int) (page []SubagentLedgerEntry, start, end int) {
	total := len(entries)
	if offset >= total {
		return nil, 0, 0
	}
	end = offset + limit
	if end > total {
		end = total
	}
	return entries[offset:end], offset + 1, end
}

// fillPageDetails completes the persisted rows of the returned page: the
// executor (from the spawn-time pin) and, when a detail source is wired, the
// summary and iteration count.
//
// Both sources are windowed on purpose. The detail source queries the session
// store, so a page of 20 rows must not cost a query over the whole history; the
// executor fallback is a map read but is applied here anyway so the tool and the
// system-prompt digest name the same agent for the same row (the digest resolves
// the pin itself). Without a wired callback the row keeps whatever the ledger
// already had.
func (t *ListSubagentsTool) fillPageDetails(page []SubagentLedgerEntry) {
	t.fillPageExecutors(page)

	detail := t.manager.PersistedTaskDetail()
	if detail == nil {
		return
	}

	keys := make([]string, 0, len(page))
	for i := range page {
		if page[i].Persisted && page[i].SessionKey != "" {
			keys = append(keys, page[i].SessionKey)
		}
	}
	if len(keys) == 0 {
		return
	}

	details := detail(keys)
	for i := range page {
		if !page[i].Persisted {
			continue
		}
		row, ok := details[page[i].SessionKey]
		if !ok {
			continue
		}
		if page[i].Summary == "" {
			page[i].Summary = row.Summary
		}
		if page[i].Iterations == 0 {
			page[i].Iterations = row.Iterations
		}
	}
}

// fillPageExecutors applies the spawn-time executor pin to persisted rows whose
// AgentID is empty — the rows written before the session row carried agent_id.
// It is a map read on the caller's side (no I/O), so it runs for the page only,
// like the detail source.
func (t *ListSubagentsTool) fillPageExecutors(page []SubagentLedgerEntry) {
	fallback := t.manager.PersistedTaskExecutorFallback()
	if fallback == nil {
		return
	}
	for i := range page {
		if !page[i].Persisted || page[i].AgentID != "" || page[i].SessionKey == "" {
			continue
		}
		page[i].AgentID = fallback(page[i].SessionKey)
	}
}

// subagentScopeKeyFromCtx derives the caller's session key.
//
// Subagent tasks record their spawning session in OriginSessionKey, built by
// BuildOriginSessionKey(channel, chatID); the caller derives its own key with
// the SAME function so both sides share one invariant by construction (the
// runtime session key alone is not comparable — it may lack the channel prefix,
// carry a ":chat:N" alias, or be a routed "agent:<id>:main" key).
//
// Precedence:
//  1. channel+chatID from the tool context — present for a normal agent turn
//     (tool_executor -> ExecuteWithContext) and for the subagent toolloop
//     (subagent_runner passes task.OriginChannel/OriginChatID, which yields the
//     parent's OriginSessionKey).
//  2. The agent-loop session key — covers CLI/tests that inject only a session
//     key. ledgerScopeMatches still matches the task's spawner key, its origin
//     key and its ":subagent-N" child key against it.
//  3. Neither -> no scoping (list all tasks), as before.
func subagentScopeKeyFromCtx(ctx context.Context) string {
	if channel, chatID := ToolContextFromCtx(ctx); channel != "" && chatID != "" {
		return BuildOriginSessionKey(channel, chatID)
	}
	if _, sessionKey := AgentToolContextFromCtx(ctx); sessionKey != "" {
		return sessionKey
	}
	return ""
}

// emptyLedgerMessage renders the case where the ledger holds no task at all.
// "No subagent tasks found." is the historical message and stays verbatim; the
// scoped variant keeps the hidden count, because a caller whose tasks all live
// in another session has an empty ledger here but a non-empty manager.
func emptyLedgerMessage(query listSubagentsQuery, scoped bool, hiddenByScope int) string {
	return emptyResultMessage(query, scoped, hiddenByScope, "No subagent tasks found.")
}

// emptyListMessage renders the "tasks exist but none match" case: the fall-through
// text differs, everything else (the hidden-by-scope branch, the status=all hint)
// is shared.
func emptyListMessage(query listSubagentsQuery, scoped bool, hiddenByScope int) string {
	return emptyResultMessage(query, scoped, hiddenByScope, emptyFilteredText(query, scoped))
}

// emptyResultMessage is the single implementation of both empty cases. They used
// to duplicate the hidden-by-scope branch verbatim, and that duplication is what
// let R2-M1 (a completion claim emitted for every filter) survive a fix to one
// copy: the two functions now differ only in the fall-through text.
//
// The hidden-by-scope branch is the same in both cases: tasks exist but belong to
// another session, so the count is the honest answer whatever the filter.
func emptyResultMessage(query listSubagentsQuery, scoped bool, hiddenByScope int, fallthroughText string) string {
	if hiddenByScope > 0 {
		if query.status == statusFilterAll {
			return fmt.Sprintf("No subagent tasks found for this session (%d task(s) from other sessions were hidden).", hiddenByScope)
		}
		return fmt.Sprintf("No active subagents in this session (%d task(s) from other sessions were hidden; use status=%s to see finished tasks).", hiddenByScope, statusFilterAll)
	}
	return fallthroughText
}

// emptyFilteredText is the fall-through of the "tasks exist but none match" case.
//
// The historical strings are kept byte-identical ONLY for the filters they were
// written for: status=all (no task at all) and status=active (nothing is
// active, so "All tasks have completed." is a fair reading of the default view).
// For any other filter the sentence would be a lie — with one RUNNING task and
// status=failed the old text answered "No active subagents. All tasks have
// completed." — so the filter is named instead. The hint always points at
// status=all, never at the filter the caller just used (a no-op suggestion) and
// never at the deprecated include_completed spelling.
func emptyFilteredText(query listSubagentsQuery, scoped bool) string {
	hint := "Use status=" + statusFilterAll + " to see other tasks."

	if query.status == statusFilterAll {
		return "No subagent tasks found."
	}
	if query.status == statusFilterActive {
		if scoped {
			return "No active subagents in this session. " + hint
		}
		return "No active subagents. All tasks have completed. " + hint
	}
	if scoped {
		return fmt.Sprintf("No tasks with status %s in this session. %s", query.status, hint)
	}
	return fmt.Sprintf("No tasks with status %s. %s", query.status, hint)
}

// renderSubagentPage renders one page of the ledger.
//
// A single page keeps the historical format byte for byte (header, one line per
// task, "\nTotal: N task(s)"): that shape is asserted by the back-compat tests
// and is what callers' prompts were tuned against. Only when the filtered set
// does not fit in one page does the header gain the window and a footer appear
// with the paging cursor.
//
// Two cases must not lie about what is shown:
//   - an offset past the end renders one honest line and NO cursor (the old
//     code printed "page 0 of 2 · next page: offset=0", a cursor pointing back
//     to page 1);
//   - a single page fetched with offset > 0 renders the paged footer, so the
//     total it reports matches the rows beside it.
func renderSubagentPage(query listSubagentsQuery, filtered, page []SubagentLedgerEntry, start, end int) string {
	total := len(filtered)

	if len(page) == 0 {
		// paginateLedger only yields an empty page for offset >= total.
		return fmt.Sprintf("Showing 0 of %d · offset %d is past the end", total, query.offset)
	}

	paged := total > query.limit || query.offset > 0

	lines := make([]string, 0, len(page)+3)
	if paged {
		lines = append(lines, fmt.Sprintf("%s %d total, showing %d-%d (newest first)",
			pageHeader(query.status), total, start, end))
	} else {
		lines = append(lines, pageHeader(query.status)+":")
	}

	for i := range page {
		lines = append(lines, renderSubagentEntry(page[i]))
	}

	if paged {
		lines = append(lines, "", pagingFooter(total, start, end, query.limit))
		return strings.Join(lines, "\n")
	}

	lines = append(lines, fmt.Sprintf("\nTotal: %d task(s)", total))
	return strings.Join(lines, "\n")
}

// pageHeader returns the header text without its trailing colon, so the
// single-page form can append ":" and the paginated form can append the window.
//
// The two historical strings are load-bearing and stay byte-identical for the
// active and all filters ("Active subagents" / "All subagent tasks"): the
// back-compat tests in list_subagents_test.go assert them. Every other filter
// names itself, because a page filtered to status=failed titled "All subagent
// tasks" reads as the whole history — the opposite of what it is.
func pageHeader(status string) string {
	switch status {
	case statusFilterActive:
		return "Active subagents"
	case statusFilterAll:
		return "All subagent tasks"
	default:
		return subagentStatusTitle(status) + " subagent tasks"
	}
}

// subagentStatusTitle renders a status value as the capitalised words used in a
// header: "failed" → "Failed", "needs_context" → "Needs context". Only the first
// word is capitalised, so the header reads as a sentence. An empty status falls
// back to its raw value so a new status can never render an empty header.
func subagentStatusTitle(status string) string {
	words := strings.FieldsFunc(status, func(r rune) bool { return r == '_' || r == '-' })
	if len(words) == 0 {
		return status
	}
	first := []rune(words[0])
	first[0] = []rune(strings.ToUpper(string(first[0])))[0]
	words[0] = string(first)
	return strings.Join(words, " ")
}

// pagingFooter renders the cursor line for a multi-page result. The "next page"
// clause is omitted on the last page, so a caller can stop paging on the footer
// alone. The page number is clamped to 1..pageCount: an empty page never
// reaches here (renderSubagentPage handles it), and a start past the end must
// not produce "page 0".
func pagingFooter(total, start, end, limit int) string {
	if limit <= 0 {
		limit = defaultSubagentListLimit
	}
	pageCount := (total + limit - 1) / limit
	if pageCount < 1 {
		pageCount = 1
	}
	pageNumber := (start + limit - 1) / limit
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageNumber > pageCount {
		pageNumber = pageCount
	}
	footer := fmt.Sprintf("Showing %d-%d of %d · page %d of %d", start, end, total, pageNumber, pageCount)
	if end < total {
		footer += fmt.Sprintf(" · next page: offset=%d&limit=%d", end, limit)
	}
	return footer
}

// Per-field byte caps for the rendered row. The row is a tool result, not the
// system prompt, but it is still one line per task and a model-authored field
// must not be able to make it unbounded or multi-line.
const (
	ledgerRowLabelMaxBytes          = 200
	ledgerRowSummaryMaxBytes        = 200
	ledgerRowProgressMaxBytes       = 120
	ledgerRowContextRequestMaxBytes = 120
)

// renderSubagentEntry renders one ledger row. The line format is the one the
// tool has always produced; a persisted row only gains " (persisted)" after the
// status bracket so a reader can tell a live task from one recovered from disk.
//
// Every model-authored field (label, summary, progress, context request) goes
// through SanitizeLedgerText: they are interpolated into a tool result the model
// reads as instructions, and a newline in one of them would open a fake row.
func renderSubagentEntry(entry SubagentLedgerEntry) string {
	line := fmt.Sprintf("- %s [%s]", entry.ID, entry.Status)
	if entry.Persisted {
		line += " (persisted)"
	}
	if label := SanitizeLedgerText(entry.Label, ledgerRowLabelMaxBytes); label != "" {
		line += " " + label
	}
	if entry.AgentID != "" {
		line += fmt.Sprintf(" (agent: %s)", entry.AgentID)
	}
	if summary := SanitizeLedgerText(entry.Summary, ledgerRowSummaryMaxBytes); summary != "" {
		line += ": " + summary
	}
	if request := SanitizeLedgerText(entry.ContextRequest, ledgerRowContextRequestMaxBytes); request != "" {
		line += " [needs: " + request + "]"
	}
	if progress := SanitizeLedgerText(entry.Progress, ledgerRowProgressMaxBytes); progress != "" {
		line += " [progress: " + progress + "]"
	}
	if entry.RetryCount > 0 {
		line += fmt.Sprintf(" (retry %d/%d)", entry.RetryCount, entry.MaxRetries)
	}
	// Elapsed time is only meaningful for a live task, and only here: the
	// system-prompt digest must stay byte-identical across turns.
	if entry.Status == SubagentStatusRunning && !entry.Persisted && entry.Created > 0 {
		elapsed := time.Since(time.UnixMilli(entry.Created)).Round(time.Second)
		line += fmt.Sprintf(" (running %s)", elapsed)
	}
	return line
}
