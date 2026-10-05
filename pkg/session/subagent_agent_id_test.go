package session

import (
	"testing"
)

// Tests for subagent executor persistence (sessions.agent_id). The WebUI names
// the agent of a HISTORICAL subagent from this column: the in-memory
// spawn-time mapping is gone after a restart, and every agent shares one
// SessionManager, so the storage owner says nothing about who ran the task.

// TestSetSubagentAgentID_PersistsAndSurvivesReload is the restart path: the
// executor must come back from SQLite through a brand-new manager, where the
// session is metadata-only (cold) and nothing is resident in memory.
func TestSetSubagentAgentID_PersistsAndSurvivesReload(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-1"
	key := parent + ":subagent-3"
	sm.AddMessage(key, "user", "do the thing")
	sm.AddMessage(key, "assistant", "done")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sm.SetSubagentAgentID(key, "coder")

	// Resident path: reported straight from the in-memory session.
	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("in-memory AgentID = %q, want %q", got, "coder")
	}

	// Cold path: a fresh manager over the same store has nothing resident and
	// answers from the persisted metadata.
	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	if got := subagentByTaskID(t, sm2.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("after reload AgentID = %q, want %q", got, "coder")
	}

	// Evicting from the first manager must not lose it either (the sidebar
	// polls long after the LRU dropped the session).
	sm.EvictSession(key)
	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("after eviction AgentID = %q, want %q", got, "coder")
	}
}

// TestSetSubagentAgentID_CreatesMissingSession pins the spawn-time contract:
// the callback runs BEFORE the subagent's first message exists, so the setter
// has to materialize the session — otherwise there is no row to carry the
// executor and the WebUI falls back to the storage owner forever.
func TestSetSubagentAgentID_CreatesMissingSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-2"
	key := parent + ":subagent-1"

	sm.SetSubagentAgentID(key, "researcher")

	if !sm.SessionExists(key) {
		t.Fatal("SetSubagentAgentID did not materialize the subagent session")
	}
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("session row not persisted")
	}
	if meta.AgentID != "researcher" {
		t.Errorf("persisted AgentID = %q, want %q", meta.AgentID, "researcher")
	}

	// The messages recorded afterwards land on the same session and keep the
	// executor (the recorder must not clobber it).
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	if got := subagentByTaskID(t, sm2.FindSubagentSessions(parent), "subagent-1").AgentID; got != "researcher" {
		t.Fatalf("AgentID after recording messages = %q, want %q", got, "researcher")
	}
}

// TestSetSubagentAgentID_OverwritesAndIgnoresEmpty covers the two guard rails:
// a re-spawn/retry may correct the executor, but an empty value (a caller that
// did not resolve a target agent) must never erase a recorded one.
func TestSetSubagentAgentID_OverwritesAndIgnoresEmpty(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-3"
	key := parent + ":subagent-2"
	sm.AddMessage(key, "user", "task")

	sm.SetSubagentAgentID(key, "coder")
	sm.SetSubagentAgentID(key, "architect")
	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-2").AgentID; got != "architect" {
		t.Fatalf("AgentID after overwrite = %q, want %q", got, "architect")
	}

	sm.SetSubagentAgentID(key, "")
	sm.SetSubagentAgentID("", "coder")
	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-2").AgentID; got != "architect" {
		t.Fatalf("AgentID after empty call = %q, want %q (must be untouched)", got, "architect")
	}
}

// TestFindSubagentSessions_AgentIDEmptyWithoutPersistence documents the
// legacy signal: a session recorded before the column existed (or with no
// store at all) reports an empty executor, which is what tells the reader to
// fall back to the storage-owning agent instead of showing a blank name.
func TestFindSubagentSessions_AgentIDEmptyWithoutPersistence(t *testing.T) {
	sm := NewSessionManager()
	parent := "native:client-4"
	sm.AddMessage(parent+":subagent-1", "user", "task")
	sm.AddMessage(parent+":subagent-1", "assistant", "done")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].AgentID != "" {
		t.Errorf("AgentID = %q, want empty for legacy sessions", results[0].AgentID)
	}
}
