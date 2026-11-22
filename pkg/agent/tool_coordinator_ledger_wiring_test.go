package agent

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

	if rows := lister([]string{"native:elsewhere"}); len(rows) != 0 {
		t.Errorf("unrelated prefix returned %d rows, want 0", len(rows))
	}
}

func TestPersistedSubagentLister_NilSessionsIsMemoryOnly(t *testing.T) {
	if lister := newPersistedSubagentLister(nil); lister != nil {
		t.Error("lister must be nil for a nil SessionManager")
	}
	if detail := newPersistedSubagentDetail(nil); detail != nil {
		t.Error("detail source must be nil for a nil SessionManager")
	}
}

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
	if rows := sessions.ListSubagentSessionMeta([]string{parent}); len(rows) != 1 || rows[0].Resident {
		t.Errorf("session became resident after listing: %+v", rows)
	}
}

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

	if got := detail(nil); len(got) != 0 {
		t.Errorf("empty request returned %d entries, want 0", len(got))
	}
}

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

	key := recordPersistedSubagent(t, sessions, "cli:direct", "subagent-1", tools.SubagentStatusCompleted)
	if !sessions.SessionExists(key) {
		t.Fatalf("SessionExists(%q) = false, want true", key)
	}
}

func TestListSubagentsTool_DiskBackedAfterCleanup(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})
	agent, sm := registerSharedForTest(t, cfg, "main")
	t.Cleanup(sm.StopRetentionCleanup)

	parent := "cli:direct"
	recordPersistedSubagent(t, agent.Sessions, parent, "subagent-1", tools.SubagentStatusCompleted)

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
	if !strings.Contains(result.ForLLM, "do the thing") {
		t.Errorf("label from the persisted session missing:\n%s", result.ForLLM)
	}
}
