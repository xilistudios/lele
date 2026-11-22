package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ListSubagentsTool struct {
	manager *SubagentManager
}

func NewListSubagentsTool(manager *SubagentManager) *ListSubagentsTool {
	return &ListSubagentsTool{manager: manager}
}

func (t *ListSubagentsTool) Name() string {
	return "list_active_subagents"
}

func (t *ListSubagentsTool) Description() string {
	return "List this session's subagent tasks, newest first, paginated with limit/offset. The ledger is complete and survives context compaction and restarts: finished tasks stay listed after their tool results are gone. Default status=active; use status=all for history. Use wait_for_subagent for the full result of a live task."
}

const statusFilterActive = "active"

const statusFilterAll = "all"

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

type listSubagentsQuery struct {
	status   string
	limit    int
	offset   int
	explicit bool
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

	memoryTasks := t.manager.ListTasks()

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

	filtered := filterLedgerByStatus(entries, query.status)

	if len(filtered) == 0 {
		if len(entries) == 0 {
			return SilentResult(emptyLedgerMessage(query, scopeKey != "", hiddenByScope))
		}
		return SilentResult(emptyListMessage(query, scopeKey != "", hiddenByScope))
	}

	page, start, end := paginateLedger(filtered, query.limit, query.offset)
	t.fillPageDetails(page)

	return SilentResult(renderSubagentPage(query, filtered, page, start, end))
}

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

func clampSubagentListLimit(limit int) int {
	if limit <= 0 {
		return defaultSubagentListLimit
	}
	if limit > maxSubagentListLimit {
		return maxSubagentListLimit
	}
	return limit
}

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

func isValidSubagentStatusFilter(status string) bool {
	for _, valid := range subagentStatusFilterValues() {
		if status == valid {
			return true
		}
	}
	return false
}

func isActiveSubagentStatus(status string) bool {
	switch status {
	case SubagentStatusRunning, SubagentStatusPending, SubagentStatusNeedsContext:
		return true
	default:
		return false
	}
}

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

func subagentScopeKeyFromCtx(ctx context.Context) string {
	if channel, chatID := ToolContextFromCtx(ctx); channel != "" && chatID != "" {
		return BuildOriginSessionKey(channel, chatID)
	}
	if _, sessionKey := AgentToolContextFromCtx(ctx); sessionKey != "" {
		return sessionKey
	}
	return ""
}

func emptyLedgerMessage(query listSubagentsQuery, scoped bool, hiddenByScope int) string {
	return emptyResultMessage(query, scoped, hiddenByScope, "No subagent tasks found.")
}

func emptyListMessage(query listSubagentsQuery, scoped bool, hiddenByScope int) string {
	return emptyResultMessage(query, scoped, hiddenByScope, emptyFilteredText(query, scoped))
}

func emptyResultMessage(query listSubagentsQuery, scoped bool, hiddenByScope int, fallthroughText string) string {
	if hiddenByScope > 0 {
		if query.status == statusFilterAll {
			return fmt.Sprintf("No subagent tasks found for this session (%d task(s) from other sessions were hidden).", hiddenByScope)
		}
		return fmt.Sprintf("No active subagents in this session (%d task(s) from other sessions were hidden; use status=%s to see finished tasks).", hiddenByScope, statusFilterAll)
	}
	return fallthroughText
}

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

func renderSubagentPage(query listSubagentsQuery, filtered, page []SubagentLedgerEntry, start, end int) string {
	total := len(filtered)

	if len(page) == 0 {
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

const (
	ledgerRowLabelMaxBytes          = 200
	ledgerRowSummaryMaxBytes        = 200
	ledgerRowProgressMaxBytes       = 120
	ledgerRowContextRequestMaxBytes = 120
)

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
	if entry.Status == SubagentStatusRunning && !entry.Persisted && entry.Created > 0 {
		elapsed := time.Since(time.UnixMilli(entry.Created)).Round(time.Second)
		line += fmt.Sprintf(" (running %s)", elapsed)
	}
	return line
}
