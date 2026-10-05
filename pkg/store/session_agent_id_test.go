package store

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Tests for the sessions.agent_id column (migration v8): it persists the agent
// that EXECUTED a session, which for subagents is the spawn's target agent and
// not the agent whose SessionManager happens to own the storage. Without it the
// WebUI shows an arbitrary agent for historical subagents.

// TestAgentID_MetaRoundTrip verifies the column survives a metadata
// write/read cycle, including the overwrite case (a re-spawn or a retry that
// re-records the executor).
func TestAgentID_MetaRoundTrip(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	meta := &SessionMeta{
		Key:       "native:client-1:subagent-7",
		Name:      "Subagent seven",
		Mode:      "agent",
		AgentID:   "coder",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.UpsertSession(*meta); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	got, err := repo.GetSessionMeta(meta.Key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if got == nil {
		t.Fatal("GetSessionMeta returned nil for existing key")
	}
	if got.AgentID != "coder" {
		t.Errorf("AgentID = %q, want %q", got.AgentID, "coder")
	}

	// Upserting the same key with another executor must overwrite it.
	meta.AgentID = "researcher"
	if err := repo.UpsertSession(*meta); err != nil {
		t.Fatalf("UpsertSession (update): %v", err)
	}
	got, err = repo.GetSessionMeta(meta.Key)
	if err != nil {
		t.Fatalf("GetSessionMeta (update): %v", err)
	}
	if got.AgentID != "researcher" {
		t.Errorf("AgentID after update = %q, want %q", got.AgentID, "researcher")
	}
}

// TestAgentID_ListSessionMetaIncludesColumn checks the list queries carry the
// column through: GetSessionMeta is not the only reader — the session manager
// builds its metadata mirror from ListSessionMeta, and the cold branch of
// FindSubagentSessions reports the executor out of that mirror.
func TestAgentID_ListSessionMetaIncludesColumn(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	mk := func(key, agentID string) SessionMeta {
		return SessionMeta{
			Key:       key,
			Mode:      "agent",
			AgentID:   agentID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
	}
	if err := repo.UpsertSession(mk("p:subagent-1", "coder")); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := repo.UpsertSession(mk("p:subagent-2", "researcher")); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	metas, err := repo.ListSessionMeta()
	if err != nil {
		t.Fatalf("ListSessionMeta: %v", err)
	}
	got := make(map[string]string, len(metas))
	for _, m := range metas {
		got[m.Key] = m.AgentID
	}
	if got["p:subagent-1"] != "coder" || got["p:subagent-2"] != "researcher" {
		t.Fatalf("ListSessionMeta agent IDs = %v, want both persisted", got)
	}

	byMode, err := repo.ListSessionMetaByMode("agent")
	if err != nil {
		t.Fatalf("ListSessionMetaByMode: %v", err)
	}
	gotMode := make(map[string]string, len(byMode))
	for _, m := range byMode {
		gotMode[m.Key] = m.AgentID
	}
	if gotMode["p:subagent-1"] != "coder" || gotMode["p:subagent-2"] != "researcher" {
		t.Fatalf("ListSessionMetaByMode agent IDs = %v, want both persisted", gotMode)
	}
}

// TestAgentID_DefaultEmpty verifies rows written without an executor
// (non-subagent sessions, and anything persisted before migration v8) read
// back empty, which is what tells the reader layer to fall back to the
// storage-owning agent.
func TestAgentID_DefaultEmpty(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	meta := SessionMeta{
		Key:       "native:client-1",
		Mode:      "agent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.UpsertSession(meta); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	got, err := repo.GetSessionMeta(meta.Key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if got.AgentID != "" {
		t.Errorf("AgentID = %q, want empty", got.AgentID)
	}
}

// TestMigrations_UpgradeV7ToV8_PreservesSessions proves the migration is safe
// on a real database: a v7 store with session rows and messages keeps every
// other column, the old rows come back with an empty agent_id (the reader's
// fallback signal), and the reopened store is writable at v8.
func TestMigrations_UpgradeV7ToV8_PreservesSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", path, err)
	}
	repo := s.Sessions()

	created := time.Now().UTC().Truncate(time.Second)
	key := "native:client-9:subagent-3"
	if err := repo.UpsertSession(SessionMeta{
		Key:             key,
		Name:            "Historical subagent",
		Mode:            "agent",
		Summary:         "did the thing",
		VerboseLevel:    "basic",
		Model:           "test:model",
		ThinkingLevel:   "high",
		Folder:          "/tmp/work",
		SubagentStatus:  "failed",
		InputTokens:     11,
		OutputTokens:    22,
		CompactionCount: 1,
		CreatedAt:       created,
		UpdatedAt:       created,
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	if err := repo.InsertMessage(key, 0, "user", `{"role":"user","content":"task"}`, false); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	// Simulate a v7 database: no agent_id column, schema version downgraded.
	db, err := openRawDB(path)
	if err != nil {
		t.Fatalf("openRawDB(%q) failed: %v", path, err)
	}
	if _, err := db.Exec(`ALTER TABLE sessions DROP COLUMN agent_id`); err != nil {
		db.Close()
		t.Fatalf("DROP agent_id: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET value = '7' WHERE key = 'schema_version'`); err != nil {
		db.Close()
		t.Fatalf("downgrade schema_version: %v", err)
	}
	db.Close()

	// Re-open: migrate 7→8 runs over the populated database.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) after downgrade failed: %v", path, err)
	}
	defer func() {
		if err := s2.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()

	var version string
	if err := s2.DB().QueryRow(
		`SELECT value FROM schema_meta WHERE key = 'schema_version'`,
	).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if want := strconv.Itoa(SchemaVersion); version != want {
		t.Errorf("schema_version = %q, want %q", version, want)
	}

	got, err := s2.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta after upgrade: %v", err)
	}
	if got == nil {
		t.Fatal("session row lost by the v7→v8 upgrade")
	}
	if got.AgentID != "" {
		t.Errorf("AgentID of a pre-v8 row = %q, want empty (reader falls back)", got.AgentID)
	}
	// Every other column must survive untouched.
	if got.Name != "Historical subagent" || got.Mode != "agent" || got.Summary != "did the thing" ||
		got.VerboseLevel != "basic" || got.Model != "test:model" || got.ThinkingLevel != "high" ||
		got.Folder != "/tmp/work" || got.SubagentStatus != "failed" ||
		got.InputTokens != 11 || got.OutputTokens != 22 || got.CompactionCount != 1 ||
		got.FirstInMemorySeq != 0 {
		t.Errorf("session metadata changed by the upgrade: %+v", got)
	}
	if !got.CreatedAt.Equal(created) || !got.UpdatedAt.Equal(created) {
		t.Errorf("timestamps changed by the upgrade: created=%v updated=%v want %v",
			got.CreatedAt, got.UpdatedAt, created)
	}

	msgs, err := s2.Sessions().LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages after upgrade: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("messages after upgrade = %d, want 1", len(msgs))
	}

	// The upgraded store is writable and now records the executor.
	got.AgentID = "coder"
	if err := s2.Sessions().UpsertSession(*got); err != nil {
		t.Fatalf("UpsertSession after upgrade: %v", err)
	}
	after, err := s2.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta after write: %v", err)
	}
	if after.AgentID != "coder" {
		t.Errorf("AgentID after post-upgrade write = %q, want %q", after.AgentID, "coder")
	}
}
