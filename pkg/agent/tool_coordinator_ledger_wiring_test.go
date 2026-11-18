package agent

// Tests for the agent-side wiring that makes the subagent ledger disk-backed.
//
// T3 gave list_active_subagents a paginated view over SubagentManager's ledger;
// until this wiring exists the ledger has no disk side and only reports live
// tasks — which is exactly the state that loses history when retention reaps a
// task or the process restarts. These tests pin:
//
//  1. the lister reads the recorder's SessionManager (the one
//     SetSessionRecorder was handed), not some other agent's;
//  2. it is metadata-only — no store query, no residency change;
//  3. the detail source is windowed to the requested keys;
//  4. the ledger and the task-ID collision probe are wired together, so a
//     manager can never list a task ID that a later spawn may reuse.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

// ledgerTestSessions builds a SessionManager backed by a temporary store, so
// the detail source has something to query.
func ledgerTestSessions(t *testing.T) *session.SessionManager {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sm := session.NewSessionManager()
	sm.SetSessionRepo(st.Sessions())
	return sm
}

// recordPersistedSubagent writes a subagent session the way the runner leaves
// one behind: messages saved under "{parent}:subagent-N", then the terminal
// status persisted on the session metadata.
func recordPersistedSubagent(t *testing.T, sessions *session.SessionManager, parent, taskID, status string) string {
	t.Helper()
	key := parent + ":" + taskID
	sessions.AddMessage(key, "user", "do the thing")
	sessions.AddMessage(key, "assistant", "done: "+taskID)
	if err := sessions.Save(key); err != nil {
		t.Fatalf("Save(%q): %v", key, err)
	}
	if status != "" {
		sessions.SetSubagentStatus(key, status)
	}
	return key
}

// TestPersistedSubagentLister_ReadsRecorderManager pins that the lister answers
// from the SessionManager it was built for, and maps every identifying field.
func TestPersistedSubagentLister_ReadsRecorderManager(t *testing.T) {
	sessions := ledgerTestSessions(t)
	parent := "native:lister"
	key := recordPersistedSubagent(t, sessions, parent, "subagent-1", tools.SubagentStatusFailed)
	sessions.SetSubagentAgentID(key, "coder")

	lister := newPersistedSubagentLister(sessions)
	if lister == nil {
		t.Fatal("lister is nil for a non-nil SessionManager")
	}

	rows := lister([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("lister returned %d rows, want 1 (%+v)", len(rows), rows)
	}
	row := rows[0]
	if row.TaskID != "subagent-1" || row.SessionKey != key || row.ParentKey != parent {
		t.Errorf("row identity = %+v, want task/session/parent of %q", row, key)
	}
	if row.Status != tools.SubagentStatusFailed {
		t.Errorf("Status = %q, want %q (persisted status must not be rewritten)", row.Status, tools.SubagentStatusFailed)
	}
	if row.AgentID != "coder" {
		t.Errorf("AgentID = %q, want %q", row.AgentID, "coder")
	}
	if row.Label == "" {
		t.Error("Label is empty, want the session name")
	}
	if row.Created == 0 || row.Updated == 0 {
		t.Errorf("timestamps = %d/%d, want unix milliseconds", row.Created, row.Updated)
	}

	// A prefix that matches nothing yields nothing.
	if rows := lister([]string{"native:elsewhere"}); len(rows) != 0 {
		t.Errorf("unrelated prefix returned %d rows, want 0", len(rows))
	}
}

// TestPersistedSubagentLister_NilSessionsIsMemoryOnly pins D13: without a
// SessionManager the callbacks stay nil, so the manager keeps its memory-only
// behaviour instead of panicking.
func TestPersistedSubagentLister_NilSessionsIsMemoryOnly(t *testing.T) {
	if lister := newPersistedSubagentLister(nil); lister != nil {
		t.Error("lister must be nil for a nil SessionManager")
	}
	if detail := newPersistedSubagentDetail(nil); detail != nil {
		t.Error("detail source must be nil for a nil SessionManager")
	}
}

// TestPersistedSubagentLister_MetadataOnlyNoResidencyChange pins the cost
// contract: the lister must not make a session resident. A cold session stays
// cold, which is what makes it safe to call on every tool execution.
func TestPersistedSubagentLister_MetadataOnlyNoResidencyChange(t *testing.T) {
	sessions := ledgerTestSessions(t)
	parent := "native:cold"
	key := recordPersistedSubagent(t, sessions, parent, "subagent-1", tools.SubagentStatusCompleted)

	if !sessions.EvictSession(key) {
		t.Fatalf("EvictSession(%q) = false", key)
	}

	lister := newPersistedSubagentLister(sessions)
	rows := lister([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("lister returned %d rows for a cold session, want 1", len(rows))
	}
	// Still cold: the metadata mirror answered, not a cold load. A residency
	// change would be visible as a second listing reporting Resident=true.
	if rows := sessions.ListSubagentSessionMeta([]string{parent}); len(rows) != 1 || rows[0].Resident {
		t.Errorf("session became resident after listing: %+v", rows)
	}
}

// TestPersistedSubagentDetail_OnlyQueriesRequestedKeys pins the windowing: the
// detail source answers exactly the keys it is given.
func TestPersistedSubagentDetail_OnlyQueriesRequestedKeys(t *testing.T) {
	sessions := ledgerTestSessions(t)
	parent := "native:detail"
	wanted := recordPersistedSubagent(t, sessions, parent, "subagent-1", tools.SubagentStatusCompleted)
	recordPersistedSubagent(t, sessions, parent, "subagent-2", tools.SubagentStatusCompleted)

	detail := newPersistedSubagentDetail(sessions)
	if detail == nil {
		t.Fatal("detail source is nil for a non-nil SessionManager")
	}

	got := detail([]string{wanted})
	if len(got) != 1 {
		t.Fatalf("detail returned %d entries, want 1 (%+v)", len(got), got)
	}
	row, ok := got[wanted]
	if !ok {
		t.Fatalf("detail missing key %q: %+v", wanted, got)
	}
	if row.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", row.Iterations)
	}
	if row.Summary == "" {
		t.Error("Summary is empty, want the last assistant text")
	}

	// An empty request must not query anything.
	if got := detail(nil); len(got) != 0 {
		t.Errorf("empty request returned %d entries, want 0", len(got))
	}
}

// TestSubagentManager_ListerAndExistsProbeWiredTogether is the R1 gate: the
// ledger and the task-ID collision probe are two halves of one invariant. A
// manager that lists persisted task IDs without probing for existing sessions
// would let a later spawn reuse an ID whose session is still on disk.
func TestSubagentManager_ListerAndExistsProbeWiredTogether(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})
	agent, sm := registerSharedForTest(t, cfg, "main")
	t.Cleanup(sm.StopRetentionCleanup)
	sessions := agent.Sessions

	if !sm.LedgerWiringEnabled() {
		t.Fatal("manager is not ledger-wired: the lister and the session-exists probe are one call, so neither half may be missing")
	}
	if sm.PersistedTaskDetail() == nil {
		t.Fatal("manager has no persisted task detail source")
	}

	// The probe must actually see a persisted subagent session, which is what
	// makes claimTaskID skip a reused ID after a restart.
	key := recordPersistedSubagent(t, sessions, "cli:direct", "subagent-1", tools.SubagentStatusCompleted)
	if !sessions.SessionExists(key) {
		t.Fatalf("SessionExists(%q) = false, want true", key)
	}
}

// TestListSubagentsTool_DiskBackedAfterCleanup is the end-to-end acceptance
// test for T4: a task that retention reaped from memory is still listed by the
// tool, through the wiring built by registerSharedToolsForAgent.
func TestListSubagentsTool_DiskBackedAfterCleanup(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})
	agent, sm := registerSharedForTest(t, cfg, "main")
	t.Cleanup(sm.StopRetentionCleanup)

	parent := "cli:direct"
	recordPersistedSubagent(t, agent.Sessions, parent, "subagent-1", tools.SubagentStatusCompleted)

	// A live task with the same key, then reaped: the row must survive the
	// transition from memory to disk without disappearing.
	finished := time.Now().Add(-time.Hour).UnixMilli()
	sm.AddTaskForTest(&tools.SubagentTask{
		ID:               "subagent-1",
		Task:             "finished task",
		AgentID:          "main",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: parent,
		Status:           tools.SubagentStatusCompleted,
		Created:          finished,
		Updated:          finished,
	}, nil)
	sm.SetRetentionPeriod(time.Millisecond)
	if removed := sm.CleanupTerminalTasks(); removed != 1 {
		t.Fatalf("CleanupTerminalTasks removed %d task(s), want 1", removed)
	}
	if tasks := sm.ListTasks(); len(tasks) != 0 {
		t.Fatalf("memory still holds %d task(s), want 0", len(tasks))
	}

	tool := tools.NewListSubagentsTool(sm)
	ctx := tools.WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]interface{}{"status": "all"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("reaped task must still be listed from disk:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "(persisted)") {
		t.Errorf("the row must be marked as persisted:\n%s", result.ForLLM)
	}
	// The windowed detail source reached the row: the label is the session name
	// derived from the first user message, which only the session metadata
	// knows (the ledger row itself carries no label for a disk row).
	if !strings.Contains(result.ForLLM, "do the thing") {
		t.Errorf("label from the persisted session missing:\n%s", result.ForLLM)
	}
}
