package store

import (
	"testing"
	"time"
)

// TestSubagentStatus_MetaRoundTrip verifies the subagent_status column
// survives a metadata write/read cycle (migration v6). This is the persistence
// backing for the WebUI subagents sidebar: without it, every evicted or
// restarted subagent falls back to "completed" regardless of its real outcome.
func TestSubagentStatus_MetaRoundTrip(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	meta := &SessionMeta{
		Key:            "native:client-1:subagent-7",
		Name:           "Subagent seven",
		Mode:           "agent",
		SubagentStatus: "failed",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
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
	if got.SubagentStatus != "failed" {
		t.Errorf("SubagentStatus = %q, want %q", got.SubagentStatus, "failed")
	}

	// Update to another terminal status; the upsert must overwrite it.
	meta.SubagentStatus = "cancelled"
	if err := repo.UpsertSession(*meta); err != nil {
		t.Fatalf("UpsertSession (update): %v", err)
	}
	got, err = repo.GetSessionMeta(meta.Key)
	if err != nil {
		t.Fatalf("GetSessionMeta (update): %v", err)
	}
	if got.SubagentStatus != "cancelled" {
		t.Errorf("SubagentStatus after update = %q, want %q", got.SubagentStatus, "cancelled")
	}
}

// TestSubagentStatus_ListSessionMetaIncludesColumn checks that the list and
// by-mode queries carry the status through — GetSessionMeta is not the only
// reader: loadSessionMetadataFromSQLite uses ListSessionMeta, and the WebUI
// session listing uses ListSessionMetaByMode.
func TestSubagentStatus_ListSessionMetaIncludesColumn(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	mk := func(key, status string) SessionMeta {
		return SessionMeta{
			Key:            key,
			Mode:           "agent",
			SubagentStatus: status,
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
	}
	if err := repo.UpsertSession(mk("p:subagent-1", "completed")); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}
	if err := repo.UpsertSession(mk("p:subagent-2", "needs_context")); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	metas, err := repo.ListSessionMeta()
	if err != nil {
		t.Fatalf("ListSessionMeta: %v", err)
	}
	got := make(map[string]string, len(metas))
	for _, m := range metas {
		got[m.Key] = m.SubagentStatus
	}
	if got["p:subagent-1"] != "completed" || got["p:subagent-2"] != "needs_context" {
		t.Fatalf("ListSessionMeta statuses = %v, want both persisted", got)
	}

	byMode, err := repo.ListSessionMetaByMode("agent")
	if err != nil {
		t.Fatalf("ListSessionMetaByMode: %v", err)
	}
	gotMode := make(map[string]string, len(byMode))
	for _, m := range byMode {
		gotMode[m.Key] = m.SubagentStatus
	}
	if gotMode["p:subagent-1"] != "completed" || gotMode["p:subagent-2"] != "needs_context" {
		t.Fatalf("ListSessionMetaByMode statuses = %v, want both persisted", gotMode)
	}
}

// TestSubagentStatus_DefaultEmpty verifies rows created without a status
// (non-subagent sessions, or anything predating migration v6) read back as
// empty, which the reader layer translates to the historical "completed".
func TestSubagentStatus_DefaultEmpty(t *testing.T) {
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
	if got.SubagentStatus != "" {
		t.Errorf("SubagentStatus = %q, want empty", got.SubagentStatus)
	}
}
