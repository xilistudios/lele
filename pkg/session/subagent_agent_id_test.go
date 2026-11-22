package session

import (
	"testing"
)

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

	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("in-memory AgentID = %q, want %q", got, "coder")
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	if got := subagentByTaskID(t, sm2.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("after reload AgentID = %q, want %q", got, "coder")
	}

	sm.EvictSession(key)
	if got := subagentByTaskID(t, sm.FindSubagentSessions(parent), "subagent-3").AgentID; got != "coder" {
		t.Fatalf("after eviction AgentID = %q, want %q", got, "coder")
	}
}

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
