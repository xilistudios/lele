package tools

// Tests for the paginated list_active_subagents view over the subagent ledger.
//
// The tool is the LLM-facing half of the ledger: it must page through a history
// that outlives context compaction, and it must keep the exact output shape the
// pre-pagination tool produced for the single-page case — list_subagents_test.go
// is the back-compat gate and is deliberately left unedited.
//
// Most tests seed the manager with AddTaskForTest instead of running real
// subagents: pagination is a pure function of the ledger, and a real spawn would
// make Created timestamps and statuses nondeterministic.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// elapsedSuffix matches the "(running 1m2s)" tail the tool appends to a live
// running task, so a format assertion does not depend on the wall clock.
var elapsedSuffix = regexp.MustCompile(` \(running [^)]*\)`)

// seedLedgerTasks registers n terminal tasks under origin, oldest first, one
// second apart so the newest-first order is unambiguous.
func seedLedgerTasks(sm *SubagentManager, origin string, n int, status string) {
	base := time.Now().Add(-time.Hour).UnixMilli()
	for i := 1; i <= n; i++ {
		addLedgerTask(sm, fmt.Sprintf("subagent-%d", i), origin, status, base+int64(i)*1000)
	}
}

// ledgerTaskIDs extracts the task IDs of the rendered rows, in output order.
func ledgerTaskIDs(output string) []string {
	var ids []string
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "- subagent-") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		if i := strings.Index(rest, " ["); i >= 0 {
			rest = rest[:i]
		}
		ids = append(ids, rest)
	}
	return ids
}

// TestListSubagents_PaginationExactWindow pins the multi-page rendering: the
// header reports the filtered total and the window, the footer carries the
// cursor for the next page.
func TestListSubagents_PaginationExactWindow(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 5, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all",
		"limit":  float64(2),
		"offset": float64(0),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	wantHeader := "All subagent tasks 5 total, showing 1-2 (newest first)"
	if !strings.HasPrefix(result.ForLLM, wantHeader) {
		t.Errorf("header = %q, want prefix %q", result.ForLLM, wantHeader)
	}
	wantFooter := "Showing 1-2 of 5 · page 1 of 3 · next page: offset=2&limit=2"
	if !strings.Contains(result.ForLLM, wantFooter) {
		t.Errorf("footer missing %q in:\n%s", wantFooter, result.ForLLM)
	}
	// Newest first: subagent-5 and subagent-4.
	if got := ledgerTaskIDs(result.ForLLM); len(got) != 2 || got[0] != "subagent-5" || got[1] != "subagent-4" {
		t.Errorf("page rows = %v, want [subagent-5 subagent-4]", got)
	}
	// A paginated page must not also print the single-page total line.
	if strings.Contains(result.ForLLM, "\nTotal:") {
		t.Errorf("multi-page output must not carry the single-page total line:\n%s", result.ForLLM)
	}
}

// TestListSubagents_PagesUnionEqualsTotalNoDupesNoGaps is the acceptance
// invariant: walking every page yields each task exactly once.
func TestListSubagents_PagesUnionEqualsTotalNoDupesNoGaps(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	const total = 7
	seedLedgerTasks(sm, origin, total, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	seen := make(map[string]int, total)
	var order []string
	for offset := 0; offset < total; offset += 3 {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"status": "all",
			"limit":  float64(3),
			"offset": float64(offset),
		})
		if result.IsError {
			t.Fatalf("offset %d: unexpected error: %s", offset, result.ForLLM)
		}
		for _, id := range ledgerTaskIDs(result.ForLLM) {
			seen[id]++
			order = append(order, id)
		}
	}

	if len(seen) != total {
		t.Errorf("union covers %d tasks, want %d (%v)", len(seen), total, seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("task %s appeared %d times across pages, want 1", id, count)
		}
	}
	// The concatenation must also be globally ordered newest-first.
	for i := 1; i < len(order); i++ {
		if order[i-1] <= order[i] {
			t.Errorf("page union is not newest-first at %d: %q then %q", i, order[i-1], order[i])
		}
	}
}

// TestListSubagents_PaginationStableOnCreatedTies pins R6: several spawns in one
// turn share a Created millisecond, so the numeric-ID tie-break is what keeps
// pagination from duplicating or dropping rows.
func TestListSubagents_PaginationStableOnCreatedTies(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	same := time.Now().Add(-time.Hour).UnixMilli()
	for i := 1; i <= 6; i++ {
		addLedgerTask(sm, fmt.Sprintf("subagent-%d", i), origin, SubagentStatusCompleted, same)
	}
	tool := NewListSubagentsTool(sm)

	seen := make(map[string]int, 6)
	for offset := 0; offset < 6; offset += 2 {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"status": "all",
			"limit":  float64(2),
			"offset": float64(offset),
		})
		for _, id := range ledgerTaskIDs(result.ForLLM) {
			seen[id]++
		}
	}
	if len(seen) != 6 {
		t.Fatalf("union covers %d tasks, want 6 (%v)", len(seen), seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("task %s appeared %d times across pages, want 1", id, count)
		}
	}
}

// TestListSubagents_OffsetBeyondTotal pins the end-of-history case: an empty
// page with ONE honest line — no page number, and above all no next-page cursor.
//
// The previous implementation rendered "page 0 of 2 · next page: offset=0", a
// cursor pointing BACK to page 1, and the old assertion pinned only the window
// so it blessed the bug.
func TestListSubagents_OffsetBeyondTotal(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 3, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all",
		"limit":  float64(2),
		"offset": float64(99),
	})
	if result.IsError {
		t.Fatalf("offset past the end must not be an error: %s", result.ForLLM)
	}
	if rows := ledgerTaskIDs(result.ForLLM); len(rows) != 0 {
		t.Errorf("rows = %v, want none", rows)
	}
	want := "Showing 0 of 3 · offset 99 is past the end"
	if result.ForLLM != want {
		t.Errorf("output = %q, want %q", result.ForLLM, want)
	}
	if strings.Contains(result.ForLLM, "next page") {
		t.Errorf("an empty page must not offer a next-page cursor:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "page 0") {
		t.Errorf("page numbers are 1-based; got 'page 0':\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Total:") {
		t.Errorf("an empty page must not print a single-page total:\n%s", result.ForLLM)
	}

	// The last page that still has rows keeps a 1-based page number and no
	// cursor.
	last := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all",
		"limit":  float64(2),
		"offset": float64(2),
	})
	if !strings.Contains(last.ForLLM, "page 2 of 2") {
		t.Errorf("last page must report 'page 2 of 2':\n%s", last.ForLLM)
	}
	if strings.Contains(last.ForLLM, "next page") {
		t.Errorf("last page must not offer a cursor:\n%s", last.ForLLM)
	}
}

// TestListSubagents_LimitClamped pins the clamping rules: a non-positive limit
// means "default", an oversized one is capped, and a wrong type is ignored.
func TestListSubagents_LimitClamped(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 150, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	cases := []struct {
		name  string
		args  map[string]interface{}
		limit int
	}{
		{"zero", map[string]interface{}{"status": "all", "limit": float64(0)}, defaultSubagentListLimit},
		{"negative", map[string]interface{}{"status": "all", "limit": float64(-1)}, defaultSubagentListLimit},
		{"above max", map[string]interface{}{"status": "all", "limit": float64(101)}, maxSubagentListLimit},
		{"huge", map[string]interface{}{"status": "all", "limit": float64(1000)}, maxSubagentListLimit},
		{"wrong type", map[string]interface{}{"status": "all", "limit": "10"}, defaultSubagentListLimit},
		{"absent", map[string]interface{}{"status": "all"}, defaultSubagentListLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), tc.args)
			if result.IsError {
				t.Fatalf("unexpected error: %s", result.ForLLM)
			}
			if got := len(ledgerTaskIDs(result.ForLLM)); got != tc.limit {
				t.Errorf("rendered %d rows, want %d", got, tc.limit)
			}
			wantFooter := fmt.Sprintf("of 150 · page 1 of %d", (150+tc.limit-1)/tc.limit)
			if !strings.Contains(result.ForLLM, wantFooter) {
				t.Errorf("footer missing %q in:\n%s", wantFooter, result.ForLLM)
			}
		})
	}

	// A negative offset is clamped to 0 rather than rejected.
	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all", "limit": float64(5), "offset": float64(-7),
	})
	if !strings.Contains(result.ForLLM, "showing 1-5") {
		t.Errorf("negative offset not clamped to 0:\n%s", result.ForLLM)
	}
}

// TestListSubagents_StatusFilterBeforePagination pins the ordering of the two
// operations: the filter runs first, so `total`, the window and the footer all
// describe the filtered set (not the raw ledger).
func TestListSubagents_StatusFilterBeforePagination(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	base := time.Now().Add(-time.Hour).UnixMilli()
	// Interleave statuses so a post-pagination filter would return a short page.
	statuses := []string{
		SubagentStatusCompleted, SubagentStatusFailed, SubagentStatusCompleted,
		SubagentStatusCancelled, SubagentStatusCompleted, SubagentStatusFailed,
	}
	for i, status := range statuses {
		addLedgerTask(sm, fmt.Sprintf("subagent-%d", i+1), origin, status, base+int64(i+1)*1000)
	}
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": SubagentStatusCompleted,
		"limit":  float64(2),
		"offset": float64(0),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "3 total, showing 1-2") {
		t.Errorf("total must count only matching rows (3), got:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "page 1 of 2") {
		t.Errorf("page count must be computed from the filtered set, got:\n%s", result.ForLLM)
	}
	for _, line := range strings.Split(result.ForLLM, "\n") {
		if strings.HasPrefix(line, "- ") && !strings.Contains(line, "[completed]") {
			t.Errorf("non-matching row rendered: %q", line)
		}
	}
}

// TestListSubagents_IncludeCompletedBackCompat pins the deprecated flag: with no
// explicit status it still means "everything".
func TestListSubagents_IncludeCompletedBackCompat(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 2, SubagentStatusCompleted)
	addLedgerTask(sm, "subagent-3", origin, SubagentStatusRunning, time.Now().UnixMilli())
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{"include_completed": true})
	if !strings.Contains(result.ForLLM, "All subagent tasks:") {
		t.Errorf("include_completed=true must use the 'All subagent tasks:' header, got:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 3 task(s)") {
		t.Errorf("include_completed=true must list all 3 tasks, got:\n%s", result.ForLLM)
	}

	// Without the flag the default is still the active set.
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "Active subagents:") {
		t.Errorf("default must use the 'Active subagents:' header, got:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1 task(s)") {
		t.Errorf("default must list only the running task, got:\n%s", result.ForLLM)
	}
}

// TestListSubagents_ExplicitStatusWinsOverIncludeCompleted pins the precedence:
// an explicit status is a more precise request than the legacy boolean.
func TestListSubagents_ExplicitStatusWinsOverIncludeCompleted(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 2, SubagentStatusCompleted)
	addLedgerTask(sm, "subagent-3", origin, SubagentStatusRunning, time.Now().UnixMilli())
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"status":            SubagentStatusRunning,
		"include_completed": true,
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1 task(s)") {
		t.Errorf("explicit status=running must win over include_completed, got:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "[completed]") {
		t.Errorf("completed rows must be filtered out, got:\n%s", result.ForLLM)
	}
}

// TestListSubagents_UnknownStatusErrors pins fail-loud: a typo must not silently
// widen the filter to "all".
func TestListSubagents_UnknownStatusErrors(t *testing.T) {
	sm := ledgerTestManager()
	seedLedgerTasks(sm, "cli:direct", 1, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{"status": "done"})
	if !result.IsError {
		t.Fatalf("unknown status must be an error, got:\n%s", result.ForLLM)
	}
	for _, want := range []string{"done", "active", "all", "completed", "failed", "cancelled", "running", "pending", "needs_context", "not_done"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("error message must name %q, got: %s", want, result.ForLLM)
		}
	}
}

// TestListSubagents_PersistedRowsMarked pins the (persisted) marker and that a
// disk row never claims to be running.
func TestListSubagents_PersistedRowsMarked(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	created := time.Now().Add(-2 * time.Hour).UnixMilli()
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{{
			TaskID:     "subagent-4",
			SessionKey: origin + ":subagent-4",
			ParentKey:  origin,
			Label:      "disk label",
			Status:     SubagentStatusCompleted,
			Created:    created,
		}}
	})
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{"status": "all"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	want := "- subagent-4 [completed] (persisted) disk label"
	if !strings.Contains(result.ForLLM, want) {
		t.Errorf("expected %q in:\n%s", want, result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "(running") {
		t.Errorf("a persisted row must not render elapsed time:\n%s", result.ForLLM)
	}
}

// TestListSubagents_PersistedFailedNotCompleted pins C3 at the rendering layer:
// the persisted status is reported verbatim, never as "completed".
func TestListSubagents_PersistedFailedNotCompleted(t *testing.T) {
	for _, status := range []string{SubagentStatusFailed, SubagentStatusCancelled, SubagentStatusNotDone} {
		t.Run(status, func(t *testing.T) {
			sm := ledgerTestManager()
			origin := "cli:direct"
			setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-1",
					SessionKey: origin + ":subagent-1",
					ParentKey:  origin,
					Status:     status,
					Created:    time.Now().Add(-time.Hour).UnixMilli(),
				}}
			})
			tool := NewListSubagentsTool(sm)

			result := tool.Execute(context.Background(), map[string]interface{}{"status": "all"})
			if !strings.Contains(result.ForLLM, "["+status+"]") {
				t.Errorf("expected [%s] in:\n%s", status, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "["+SubagentStatusCompleted+"]") {
				t.Errorf("persisted %s row misreported as completed:\n%s", status, result.ForLLM)
			}
		})
	}
}

// TestListSubagents_DiskRowsAfterCleanupAndRestart is acceptance 2: a task that
// retention reaped (and whose manager was then replaced, as a restart does) is
// still listed.
func TestListSubagents_DiskRowsAfterCleanupAndRestart(t *testing.T) {
	origin := "cli:direct"
	finished := time.Now().Add(-time.Hour).UnixMilli()

	// The disk side is what survives: a lister that reports the reaped task.
	lister := func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{{
			TaskID:     "subagent-1",
			SessionKey: origin + ":subagent-1",
			ParentKey:  origin,
			Label:      "survivor",
			Status:     SubagentStatusCompleted,
			Created:    finished,
		}}
	}

	// Phase 1: live manager, task reaped by retention.
	sm := ledgerTestManager()
	setTestLedgerLister(sm, lister)
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, finished)
	sm.SetRetentionPeriod(time.Millisecond)
	if removed := sm.CleanupTerminalTasks(); removed != 1 {
		t.Fatalf("CleanupTerminalTasks removed %d, want 1", removed)
	}
	if tasks := sm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("memory still holds %d tasks", len(tasks))
	}
	result := NewListSubagentsTool(sm).Execute(context.Background(), map[string]interface{}{"status": "all"})
	if !strings.Contains(result.ForLLM, "survivor") {
		t.Errorf("reaped task must still be listed from disk:\n%s", result.ForLLM)
	}

	// Phase 2: restart — a brand-new manager with only the disk source.
	restarted := ledgerTestManager()
	setTestLedgerLister(restarted, lister)
	result = NewListSubagentsTool(restarted).Execute(context.Background(), map[string]interface{}{"status": "all"})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "(persisted)") {
		t.Errorf("task must survive a restart via the disk ledger:\n%s", result.ForLLM)
	}
}

// TestListSubagents_SinglePageFormatUnchanged pins D10: when everything fits,
// the output is exactly what the pre-pagination tool produced.
func TestListSubagents_SinglePageFormatUnchanged(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	created := time.Now().Add(-time.Minute).UnixMilli()
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusCompleted, created)
	addLedgerTask(sm, "subagent-2", origin, SubagentStatusRunning, created+1000)
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{"status": "all"})
	// The elapsed suffix is wall-clock dependent, so it is matched separately;
	// everything else must be byte-identical.
	want := "All subagent tasks:\n" +
		"- subagent-2 [running] label subagent-2 (agent: coder): summary subagent-2\n" +
		"- subagent-1 [completed] label subagent-1 (agent: coder): summary subagent-1\n" +
		"\nTotal: 2 task(s)"
	got := elapsedSuffix.ReplaceAllString(result.ForLLM, "")
	if got != want {
		t.Errorf("single-page output changed:\n got: %q\nwant: %q", got, want)
	}

	// The active-only single page keeps its historical header too.
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.HasPrefix(result.ForLLM, "Active subagents:\n") || !strings.HasSuffix(result.ForLLM, "\nTotal: 1 task(s)") {
		t.Errorf("active single-page shape changed:\n%s", result.ForLLM)
	}
}

// TestListSubagents_DetailFillsPageOnly pins the windowed detail source: only
// the returned page is queried, and its summary/iterations reach the rows.
func TestListSubagents_DetailFillsPageOnly(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	base := time.Now().Add(-time.Hour).UnixMilli()
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		rows := make([]PersistedSubagentTask, 0, 5)
		for i := 1; i <= 5; i++ {
			rows = append(rows, PersistedSubagentTask{
				TaskID:     fmt.Sprintf("subagent-%d", i),
				SessionKey: fmt.Sprintf("%s:subagent-%d", origin, i),
				ParentKey:  origin,
				Status:     SubagentStatusCompleted,
				Created:    base + int64(i)*1000,
			})
		}
		return rows
	})

	var queried []string
	sm.SetPersistedTaskDetail(func(keys []string) map[string]PersistedSubagentDetail {
		queried = append(queried, keys...)
		out := make(map[string]PersistedSubagentDetail, len(keys))
		for _, key := range keys {
			out[key] = PersistedSubagentDetail{Iterations: 3, Summary: "detail for " + key}
		}
		return out
	})

	tool := NewListSubagentsTool(sm)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all",
		"limit":  float64(2),
		"offset": float64(0),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if len(queried) != 2 {
		t.Errorf("detail source queried %d keys, want 2 (the page only): %v", len(queried), queried)
	}
	if !strings.Contains(result.ForLLM, "detail for "+origin+":subagent-5") {
		t.Errorf("summary from the detail source missing:\n%s", result.ForLLM)
	}

	// Without a detail source the row still renders, label-less if none.
	sm.SetPersistedTaskDetail(nil)
	result = tool.Execute(context.Background(), map[string]interface{}{"status": "all", "limit": float64(1)})
	if result.IsError {
		t.Fatalf("nil detail source must not fail: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "detail for") {
		t.Errorf("stale detail leaked after clearing the source:\n%s", result.ForLLM)
	}
}

// TestListSubagents_ScopingHidesCountOverMemoryOnly pins that the hidden count
// is computed over memory tasks: a disk row is already prefix-scoped, so it can
// never be "hidden by scope" and must not inflate the number.
func TestListSubagents_ScopingHidesCountOverMemoryOnly(t *testing.T) {
	sm := ledgerTestManager()
	// One live task of ANOTHER session (counts as hidden) ...
	addLedgerTask(sm, "subagent-1", "native:other", SubagentStatusRunning, time.Now().UnixMilli())
	// ... and a disk row of another session that the lister (correctly) never
	// returns for this scope, plus one of our own.
	setTestLedgerLister(sm, func(prefixes []string) []PersistedSubagentTask {
		for _, prefix := range prefixes {
			if prefix == "native:mine" {
				return []PersistedSubagentTask{{
					TaskID:     "subagent-9",
					SessionKey: "native:mine:subagent-9",
					ParentKey:  "native:mine",
					Status:     SubagentStatusCompleted,
					Created:    time.Now().Add(-time.Hour).UnixMilli(),
				}}
			}
		}
		return nil
	})

	tool := NewListSubagentsTool(sm)
	ctx := WithAgentToolContext(context.Background(), "test-agent", "native:mine")

	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("expected the scoped empty message, got:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "1 task(s) from other sessions were hidden") {
		t.Errorf("hidden count must be 1 (memory only), got:\n%s", result.ForLLM)
	}

	// With status=all our own disk row is visible and nothing is hidden.
	result = tool.Execute(ctx, map[string]interface{}{"status": "all"})
	if !strings.Contains(result.ForLLM, "subagent-9") {
		t.Errorf("own persisted row missing:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("another session's task leaked:\n%s", result.ForLLM)
	}
}

// TestListSubagents_DescriptionStaysShort guards the system-prompt budget: the
// description is echoed verbatim by ToolRegistry.GetSummaries.
func TestListSubagents_DescriptionStaysShort(t *testing.T) {
	desc := NewListSubagentsTool(nil).Description()
	if len(desc) >= 500 {
		t.Errorf("Description is %d chars, must stay under 500", len(desc))
	}
	if !strings.Contains(desc, "paginated") || !strings.Contains(desc, "status=all") {
		t.Errorf("Description must advertise pagination and status=all: %s", desc)
	}
}

// TestListSubagents_ParametersSchema pins the advertised schema: the enum, the
// bounds and the absence of the deferred include_results knob.
func TestListSubagents_ParametersSchema(t *testing.T) {
	params := NewListSubagentsTool(nil).Parameters()
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties missing: %v", params)
	}
	for _, name := range []string{"status", "limit", "offset", "include_completed"} {
		if _, ok := props[name]; !ok {
			t.Errorf("parameter %q missing", name)
		}
	}
	if _, ok := props["include_results"]; ok {
		t.Error("include_results is deferred and must not be advertised")
	}

	status, _ := props["status"].(map[string]interface{})
	enum, _ := status["enum"].([]string)
	// "unknown" is appended last: it is the status of a persisted row whose
	// outcome was never recorded, and it must be selectable explicitly.
	want := []string{"active", "all", "running", "pending", "needs_context", "completed", "failed", "cancelled", "not_done", "unknown"}
	if len(enum) != len(want) {
		t.Fatalf("status enum = %v, want %v", enum, want)
	}
	for i := range want {
		if enum[i] != want[i] {
			t.Errorf("status enum[%d] = %q, want %q", i, enum[i], want[i])
		}
	}

	limit, _ := props["limit"].(map[string]interface{})
	if limit["minimum"] != 1 || limit["maximum"] != maxSubagentListLimit {
		t.Errorf("limit bounds = %v/%v, want 1/%d", limit["minimum"], limit["maximum"], maxSubagentListLimit)
	}
	offset, _ := props["offset"].(map[string]interface{})
	if offset["minimum"] != 0 {
		t.Errorf("offset minimum = %v, want 0", offset["minimum"])
	}
}

// TestListSubagents_ConcurrentPagesAreConsistent is the concurrency guard: pages
// fetched while tasks are being added must never repeat a row within one page.
func TestListSubagents_ConcurrentPagesAreConsistent(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 10, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 11; i <= 30; i++ {
			addLedgerTask(sm, fmt.Sprintf("subagent-%d", i), origin, SubagentStatusCompleted, time.Now().UnixMilli())
		}
	}()

	for round := 0; round < 20; round++ {
		result := tool.Execute(context.Background(), map[string]interface{}{
			"status": "all",
			"limit":  float64(5),
			"offset": float64(round % 5),
		})
		if result.IsError {
			t.Fatalf("round %d: unexpected error: %s", round, result.ForLLM)
		}
		seen := make(map[string]bool)
		for _, id := range ledgerTaskIDs(result.ForLLM) {
			if seen[id] {
				t.Errorf("round %d: duplicate row %s in one page", round, id)
			}
			seen[id] = true
		}
	}
	<-done
}

// pageTotal parses the "total" a call reports, from either output shape: the
// paginated header ("All subagent tasks 57 total, showing …") or the
// single-page footer ("Total: N task(s)"). It fails when neither is present, so
// a call that silently changed shape cannot be mistaken for a consistent one.
func pageTotal(t *testing.T, output string) int {
	t.Helper()
	header := regexp.MustCompile(`^All subagent tasks (\d+) total, showing \d+-\d+ \(newest first\)$`)
	for _, line := range strings.Split(output, "\n") {
		if m := header.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatalf("unparseable header total %q: %v", m[1], err)
			}
			return n
		}
	}
	footer := regexp.MustCompile(`(?m)^Total: (\d+) task\(s\)$`)
	if m := footer.FindStringSubmatch(output); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unparseable footer total %q: %v", m[1], err)
		}
		return n
	}
	t.Fatalf("output reports no total:\n%s", output)
	return 0
}

// TestListSubagents_PaginationInvariantUnderConcurrentSpawn is the concurrency
// contract of the paginated ledger, run under -race.
//
// Invariant pinned (per call, never across calls): a page never repeats a task
// ID, every row of every page belongs to the filtered set, and the footer's
// total equals the number of rows the SAME call filtered. Snapshots may differ
// between calls — tasks are being spawned meanwhile, so `total` legitimately
// grows — but a single call must be internally consistent, because the model
// reads one call at a time and pages by its footer.
//
// The spawner writes through the same manager the tool reads, which is what
// makes this a race test and not just a loop: LedgerEntries snapshots memory
// under RLock and then invokes the disk lister outside every lock, so a
// concurrent task map mutation is the exact hazard being exercised.
func TestListSubagents_PaginationInvariantUnderConcurrentSpawn(t *testing.T) {
	sm := ledgerTestManager()
	const origin = "cli:direct"
	const seeded = 12
	seedLedgerTasks(sm, origin, seeded, SubagentStatusCompleted)

	// The disk side answers from a snapshot the spawner also extends, so the
	// memory and disk halves move at the same time.
	var diskMu sync.Mutex
	diskRows := make([]PersistedSubagentTask, 0, 64)
	base := time.Now().Add(-2 * time.Hour).UnixMilli()
	for i := 1; i <= seeded; i++ {
		diskRows = append(diskRows, PersistedSubagentTask{
			TaskID:     fmt.Sprintf("subagent-%d", i),
			SessionKey: fmt.Sprintf("%s:subagent-%d", origin, i),
			ParentKey:  origin,
			Status:     SubagentStatusCompleted,
			Created:    base + int64(i)*1000,
		})
	}
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		diskMu.Lock()
		defer diskMu.Unlock()
		return append([]PersistedSubagentTask(nil), diskRows...)
	})

	tool := NewListSubagentsTool(sm)
	ctx := WithToolContext(context.Background(), "cli", "direct")

	// The spawner publishes to the disk half only AFTER the memory half is
	// registered, and only for tasks the memory half already holds. That is the
	// order a real spawn produces (the task is created, then its session is
	// written), and it is what makes the per-call assertions below exact: a
	// task that appears on disk but not yet in memory would legitimately shift
	// a page between the two reads inside one call, which is the documented
	// drift (plan §11 Q5) rather than the invariant under test.
	stop := make(chan struct{})
	spawned := make(chan struct{})
	go func() {
		defer close(spawned)
		for i := seeded + 1; i <= seeded+40; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("subagent-%d", i)
			created := time.Now().UnixMilli()
			addLedgerTask(sm, id, origin, SubagentStatusCompleted, created)
			diskMu.Lock()
			diskRows = append(diskRows, PersistedSubagentTask{
				TaskID:     id,
				SessionKey: origin + ":" + id,
				ParentKey:  origin,
				Status:     SubagentStatusCompleted,
				Created:    created,
			})
			diskMu.Unlock()
			time.Sleep(time.Duration(i%3) * 100 * time.Microsecond)
		}
	}()

	// Take pages while the spawner runs. Every call is checked on its own.
	for round := 0; round < 60; round++ {
		limit := 1 + round%5
		offset := round % 7
		result := tool.Execute(ctx, map[string]interface{}{
			"status": "all",
			"limit":  float64(limit),
			"offset": float64(offset),
		})
		if result.IsError {
			t.Fatalf("round %d: unexpected error: %s", round, result.ForLLM)
		}

		ids := ledgerTaskIDs(result.ForLLM)
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("round %d: duplicate row %s within one page:\n%s", round, id, result.ForLLM)
			}
			seen[id] = true
			// Every row must belong to the filtered set: status=all, so the
			// only way a row can be foreign is a scoping leak.
			if !strings.HasPrefix(id, "subagent-") {
				t.Errorf("round %d: row %q is not a subagent task ID:\n%s", round, id, result.ForLLM)
			}
		}
		if len(ids) > limit {
			t.Errorf("round %d: page returned %d rows for limit %d", round, len(ids), limit)
		}

		// Per-call consistency: the reported total must match the rows the same
		// call filtered, which is what makes the footer a usable cursor.
		total := pageTotal(t, result.ForLLM)
		if offset >= total {
			if len(ids) != 0 {
				t.Errorf("round %d: offset %d past total %d returned %d rows", round, offset, total, len(ids))
			}
			continue
		}
		wantRows := total - offset
		if wantRows > limit {
			wantRows = limit
		}
		if len(ids) != wantRows {
			t.Errorf("round %d: page has %d rows, want %d (total=%d offset=%d limit=%d):\n%s",
				round, len(ids), wantRows, total, offset, limit, result.ForLLM)
		}
		// A "next page" footer must point at a page that still has rows.
		if strings.Contains(result.ForLLM, "next page: offset=") && offset+len(ids) >= total {
			t.Errorf("round %d: footer offers a next page past the end (total=%d, shown %d-%d):\n%s",
				round, total, offset+1, offset+len(ids), result.ForLLM)
		}
	}

	close(stop)
	<-spawned

	// After the spawner stops, the union of pages must still be exact: the
	// concurrency must not have corrupted the order or the dedupe.
	final := tool.Execute(ctx, map[string]interface{}{"status": "all", "limit": float64(1)})
	total := pageTotal(t, final.ForLLM)
	seen := make(map[string]bool, total)
	for offset := 0; offset < total; offset += 5 {
		page := tool.Execute(ctx, map[string]interface{}{
			"status": "all",
			"limit":  float64(5),
			"offset": float64(offset),
		})
		for _, id := range ledgerTaskIDs(page.ForLLM) {
			if seen[id] {
				t.Fatalf("task %s appeared twice across the final page walk", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != total {
		t.Errorf("final page walk covered %d tasks, footer says %d", len(seen), total)
	}
}

// TestListSubagents_HeaderNamesFilter pins MENOR-3: a filtered page must not be
// titled as the whole history. The two historical strings stay byte-identical
// for the active and all filters (list_subagents_test.go is the gate).
func TestListSubagents_HeaderNamesFilter(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	base := time.Now().Add(-time.Hour).UnixMilli()
	// One row per status the header must name.
	for i, status := range []string{
		SubagentStatusFailed,
		SubagentStatusRunning,
		SubagentStatusNeedsContext,
		SubagentStatusNotDone,
		SubagentStatusCompleted,
	} {
		addLedgerTask(sm, fmt.Sprintf("subagent-%d", i+1), origin, status, base+int64(i+1)*1000)
	}
	tool := NewListSubagentsTool(sm)

	cases := []struct {
		status string
		header string
	}{
		{SubagentStatusFailed, "Failed subagent tasks:"},
		{SubagentStatusRunning, "Running subagent tasks:"},
		{SubagentStatusNeedsContext, "Needs context subagent tasks:"},
		{SubagentStatusNotDone, "Not done subagent tasks:"},
		{SubagentStatusCompleted, "Completed subagent tasks:"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]interface{}{"status": tc.status})
			if result.IsError {
				t.Fatalf("unexpected error: %s", result.ForLLM)
			}
			if !strings.HasPrefix(result.ForLLM, tc.header+"\n") {
				t.Errorf("header = %q, want prefix %q", result.ForLLM, tc.header)
			}
			if strings.Contains(result.ForLLM, "All subagent tasks") {
				t.Errorf("a filtered page must not claim to be the whole history:\n%s", result.ForLLM)
			}
		})
	}

	// The historical strings are unchanged for the two unfiltered views.
	if got := pageHeader(statusFilterActive); got != "Active subagents" {
		t.Errorf("pageHeader(active) = %q, want %q", got, "Active subagents")
	}
	if got := pageHeader(statusFilterAll); got != "All subagent tasks" {
		t.Errorf("pageHeader(all) = %q, want %q", got, "All subagent tasks")
	}

	// The paginated variant names the filter too.
	seedLedgerTasks(sm, origin, 5, SubagentStatusFailed)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": SubagentStatusFailed,
		"limit":  float64(2),
	})
	if !strings.HasPrefix(result.ForLLM, "Failed subagent tasks 5 total, showing 1-2") {
		t.Errorf("paginated header must name the filter:\n%s", result.ForLLM)
	}
}

// TestListSubagents_EmptyHintPointsAtStatusAll pins MENOR-2: the hint must not
// suggest the filter the caller just used, and must not use the deprecated
// include_completed spelling that Description() deprecates.
func TestListSubagents_EmptyHintPointsAtStatusAll(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 2, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	// status=failed with no failed task: the old message said
	// "Use status=failed to see other tasks." — a no-op suggestion.
	result := tool.Execute(context.Background(), map[string]interface{}{"status": SubagentStatusFailed})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "status=failed") {
		t.Errorf("hint repeats the filter the caller used:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "status=all") {
		t.Errorf("hint must point at status=all:\n%s", result.ForLLM)
	}

	// The default (active) filter path.
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if strings.Contains(result.ForLLM, "include_completed") {
		t.Errorf("hint uses the deprecated include_completed spelling:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "status=all") {
		t.Errorf("hint must point at status=all:\n%s", result.ForLLM)
	}

	// The scoped + hidden branch, which hardcoded the deprecated spelling.
	ctx := WithAgentToolContext(context.Background(), "test-agent", "native:mine")
	result = tool.Execute(ctx, map[string]interface{}{})
	if strings.Contains(result.ForLLM, "include_completed") {
		t.Errorf("scoped hint uses the deprecated include_completed spelling:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "status=all") {
		t.Errorf("scoped hint must point at status=all:\n%s", result.ForLLM)
	}
}

// TestListSubagents_EmptyMessageNamesTheFilter pins R2-M1: the historical
// "All tasks have completed." claim was emitted for EVERY filter, so with one
// running task and status=failed the tool answered "No active subagents. All
// tasks have completed." — two false statements in one line.
//
// Invariant pinned: the completion claim survives byte-identically for the
// default (active) filter, which is the view it was written for, and never
// appears for any other filter; those name the filter instead.
func TestListSubagents_EmptyMessageNamesTheFilter(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	// One RUNNING task: there is an active subagent, and tasks have not all
	// completed, so both halves of the old sentence were wrong.
	addLedgerTask(sm, "subagent-1", origin, SubagentStatusRunning, time.Now().UnixMilli())
	tool := NewListSubagentsTool(sm)

	for _, status := range []string{
		SubagentStatusFailed,
		SubagentStatusCompleted,
		SubagentStatusCancelled,
		SubagentStatusNotDone,
		SubagentStatusPending,
		SubagentStatusNeedsContext,
		SubagentStatusUnknown,
	} {
		t.Run(status, func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]interface{}{"status": status})
			if result.IsError {
				t.Fatalf("unexpected error: %s", result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "All tasks have completed") {
				t.Errorf("status=%s claims every task completed while one is running:\n%s", status, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "No active subagents") {
				t.Errorf("status=%s answers the default view instead of the filter:\n%s", status, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, status) {
				t.Errorf("status=%s does not name the filter:\n%s", status, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "status="+statusFilterAll) {
				t.Errorf("status=%s lost the status=all hint:\n%s", status, result.ForLLM)
			}
		})
	}

	// The scoped variant names the filter too. The running task is scoped to
	// this session, so the hidden-by-scope branch is not what answers here.
	scopedSM := ledgerTestManager()
	addLedgerTask(scopedSM, "subagent-1", "native:mine", SubagentStatusRunning, time.Now().UnixMilli())
	scopedTool := NewListSubagentsTool(scopedSM)
	ctx := WithAgentToolContext(context.Background(), "test-agent", "native:mine")
	result := scopedTool.Execute(ctx, map[string]interface{}{"status": SubagentStatusFailed})
	if strings.Contains(result.ForLLM, "All tasks have completed") {
		t.Errorf("scoped status=failed claims every task completed:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, SubagentStatusFailed) || !strings.Contains(result.ForLLM, "this session") {
		t.Errorf("scoped message must name the filter and the scope:\n%s", result.ForLLM)
	}

	// The historical string stays byte-identical for the default filter, which
	// is the only view it describes correctly.
	completed := ledgerTestManager()
	addLedgerTask(completed, "subagent-1", origin, SubagentStatusCompleted, time.Now().UnixMilli())
	result = NewListSubagentsTool(completed).Execute(context.Background(), map[string]interface{}{})
	const want = "No active subagents. All tasks have completed. Use status=all to see other tasks."
	if result.ForLLM != want {
		t.Errorf("default-filter message = %q, want the historical %q", result.ForLLM, want)
	}
	scopedCompleted := ledgerTestManager()
	addLedgerTask(scopedCompleted, "subagent-1", "native:mine", SubagentStatusCompleted, time.Now().UnixMilli())
	result = NewListSubagentsTool(scopedCompleted).Execute(ctx, map[string]interface{}{})
	const wantScoped = "No active subagents in this session. Use status=all to see other tasks."
	if result.ForLLM != wantScoped {
		t.Errorf("scoped default-filter message = %q, want the historical %q", result.ForLLM, wantScoped)
	}
}

// TestListSubagents_EmptyMessagesShareTheHiddenBranch pins R2-N4: the two empty
// messages differ only in their fall-through text, so the hidden-by-scope branch
// (which used to be duplicated verbatim, and is what let R2-M1 survive a fix to
// one copy) must be identical for both.
func TestListSubagents_EmptyMessagesShareTheHiddenBranch(t *testing.T) {
	for _, status := range []string{statusFilterActive, statusFilterAll, SubagentStatusFailed} {
		for _, hidden := range []int{1, 3} {
			query := listSubagentsQuery{status: status}
			ledgerEmpty := emptyLedgerMessage(query, true, hidden)
			listEmpty := emptyListMessage(query, true, hidden)
			if ledgerEmpty != listEmpty {
				t.Errorf("status=%s hidden=%d: empty-ledger %q differs from empty-list %q",
					status, hidden, ledgerEmpty, listEmpty)
			}
		}
	}

	// Without hidden tasks the two still differ: the ledger is empty, the
	// filtered view had tasks that did not match.
	query := listSubagentsQuery{status: SubagentStatusFailed}
	if got := emptyLedgerMessage(query, false, 0); got != "No subagent tasks found." {
		t.Errorf("empty ledger message = %q", got)
	}
	if got := emptyListMessage(query, false, 0); !strings.Contains(got, SubagentStatusFailed) {
		t.Errorf("empty list message must name the filter: %q", got)
	}
}

// TestListSubagents_ExecutorFallbackApplied pins MENOR-4: the tool resolves a
// persisted row's executor through the spawn-time pin, exactly like the digest,
// so one row cannot name an agent in the prompt and no agent in the tool.
func TestListSubagents_ExecutorFallbackApplied(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{
			{TaskID: "subagent-1", SessionKey: origin + ":subagent-1", ParentKey: origin, Status: SubagentStatusCompleted},
			{TaskID: "subagent-2", SessionKey: origin + ":subagent-2", ParentKey: origin, Status: SubagentStatusCompleted, AgentID: "reviewer"},
		}
	})

	var asked []string
	sm.SetPersistedTaskExecutorFallback(func(sessionKey string) string {
		asked = append(asked, sessionKey)
		if sessionKey == origin+":subagent-1" {
			return "coder"
		}
		return ""
	})

	tool := NewListSubagentsTool(sm)
	result := tool.Execute(context.Background(), map[string]interface{}{"status": "all"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "- subagent-1 [completed] (persisted) (agent: coder)") {
		t.Errorf("row without agent_id must gain the pinned executor:\n%s", result.ForLLM)
	}
	// A row that already carries an executor is never overridden, and is not
	// even asked about.
	if !strings.Contains(result.ForLLM, "(agent: reviewer)") {
		t.Errorf("stored executor must survive:\n%s", result.ForLLM)
	}
	for _, key := range asked {
		if key == origin+":subagent-2" {
			t.Errorf("fallback was consulted for a row that already has an executor: %v", asked)
		}
	}

	// Without a wired fallback the row keeps its empty AgentID (standalone
	// managers in tests).
	sm.SetPersistedTaskExecutorFallback(nil)
	result = tool.Execute(context.Background(), map[string]interface{}{"status": "all"})
	if strings.Contains(result.ForLLM, "(agent: coder)") {
		t.Errorf("stale executor leaked after clearing the fallback:\n%s", result.ForLLM)
	}
}

// TestListSubagents_OffsetWithinSinglePage pins NIT-4: a single page fetched
// with offset > 0 must not print a total that disagrees with the rows beside
// it.
func TestListSubagents_OffsetWithinSinglePage(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	seedLedgerTasks(sm, origin, 3, SubagentStatusCompleted)
	tool := NewListSubagentsTool(sm)

	result := tool.Execute(context.Background(), map[string]interface{}{
		"status": "all",
		"limit":  float64(20),
		"offset": float64(1),
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Total: 3 task(s)") {
		t.Errorf("a page showing 2 of 3 rows must not claim 'Total: 3 task(s)':\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "All subagent tasks 3 total, showing 2-3") {
		t.Errorf("offset > 0 must use the paged header:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Showing 2-3 of 3 · page 1 of 1") {
		t.Errorf("offset > 0 must use the paged footer:\n%s", result.ForLLM)
	}
	if rows := ledgerTaskIDs(result.ForLLM); len(rows) != 2 {
		t.Errorf("rows = %v, want 2", rows)
	}
}

// TestListSubagents_SnapshotTakenOnce pins NIT-3: the hidden-by-scope count and
// the rendered rows come from ONE task snapshot, so a concurrent reap cannot
// make them disagree.
func TestListSubagents_SnapshotTakenOnce(t *testing.T) {
	sm := ledgerTestManager()
	// Two live tasks of another session, and one of ours.
	addLedgerTask(sm, "subagent-1", "native:other", SubagentStatusRunning, 3000)
	addLedgerTask(sm, "subagent-2", "native:other", SubagentStatusRunning, 2000)
	addLedgerTask(sm, "subagent-3", "native:mine", SubagentStatusRunning, 1000)

	tool := NewListSubagentsTool(sm)
	ctx := WithAgentToolContext(context.Background(), "test-agent", "native:mine")
	result := tool.Execute(ctx, map[string]interface{}{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-3") {
		t.Errorf("own task missing:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-1") || strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("another session's task leaked:\n%s", result.ForLLM)
	}
}

// TestListSubagents_UnknownStatusSelectable pins M2 at the tool layer: a
// persisted row with no recorded status is listed as [unknown], is NOT part of
// the active set, and can be selected explicitly.
func TestListSubagents_UnknownStatusSelectable(t *testing.T) {
	sm := ledgerTestManager()
	origin := "cli:direct"
	setTestLedgerLister(sm, func([]string) []PersistedSubagentTask {
		return []PersistedSubagentTask{
			{TaskID: "subagent-1", SessionKey: origin + ":subagent-1", ParentKey: origin, Status: ""},
		}
	})
	tool := NewListSubagentsTool(sm)

	// Not active: an interrupted task is not something that can produce a
	// result, and it must not be reported as completed either.
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("an unknown row must not satisfy the active filter:\n%s", result.ForLLM)
	}

	// Selectable explicitly, and rendered as unknown.
	result = tool.Execute(context.Background(), map[string]interface{}{"status": SubagentStatusUnknown})
	if result.IsError {
		t.Fatalf("status=unknown must be a valid filter: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "- subagent-1 [unknown] (persisted)") {
		t.Errorf("expected an [unknown] row:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "[completed]") {
		t.Errorf("an interrupted task must never render as completed:\n%s", result.ForLLM)
	}
	if !strings.HasPrefix(result.ForLLM, "Unknown subagent tasks:") {
		t.Errorf("header must name the unknown filter:\n%s", result.ForLLM)
	}
}
