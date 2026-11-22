package session

import (
	"strings"
	"testing"
	"time"
)

func TestSetSubagentStatus_PersistsAndSurvivesReload(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:subagent-3"
	sm.AddMessage(key, "user", "do the thing")
	sm.AddMessage(key, "assistant", "I could not find the file.")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sm.SetSubagentStatus(key, "failed")

	for _, past := range sm.FindSubagentSessions("native:client-1") {
		if past.TaskID == "subagent-3" && past.Status != "failed" {
			t.Fatalf("in-memory status = %q, want %q", past.Status, "failed")
		}
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	results := sm2.FindSubagentSessions("native:client-1")
	var status string
	for _, past := range results {
		if past.TaskID == "subagent-3" {
			status = past.Status
		}
	}
	if status != "failed" {
		t.Fatalf("after reload status = %q, want %q (results: %+v)", status, "failed", results)
	}
}

func TestSetSubagentStatus_UnknownKeyIsNoop(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	sm.SetSubagentStatus("native:ghost:subagent-1", "completed")

	if sm.SessionExists("native:ghost:subagent-1") {
		t.Fatal("SetSubagentStatus must not materialize an unknown session")
	}
}

func TestFindSubagentSessions_StatusEmptyWithoutPersistence(t *testing.T) {
	sm := NewSessionManager()
	parent := "native:client-1"
	sm.AddMessage(parent+":subagent-1", "user", "task")
	sm.AddMessage(parent+":subagent-1", "assistant", "done")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != "" {
		t.Errorf("Status = %q, want empty for legacy sessions", results[0].Status)
	}
}

func TestFindSubagentSessions_StatusAcrossManyTerminals(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:client-1"
	statuses := map[string]string{
		"subagent-1": "completed",
		"subagent-2": "failed",
		"subagent-3": "not_done",
		"subagent-4": "cancelled",
		"subagent-5": "needs_context",
	}
	for taskID := range statuses {
		key := parent + ":" + taskID
		sm.AddMessage(key, "user", "task")
		sm.AddMessage(key, "assistant", "answer")
	}
	for taskID, status := range statuses {
		sm.SetSubagentStatus(parent+":"+taskID, status)
	}

	results := sm.FindSubagentSessions(parent)
	if len(results) != len(statuses) {
		t.Fatalf("expected %d results, got %d", len(statuses), len(results))
	}
	for _, past := range results {
		want, ok := statuses[past.TaskID]
		if !ok {
			t.Fatalf("unexpected task %q", past.TaskID)
		}
		if past.Status != want {
			t.Errorf("%s status = %q, want %q", past.TaskID, past.Status, want)
		}
	}
}

func TestSetSubagentStatus_SyncsSessionAndMeta(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:subagent-9"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", strings.Repeat("result ", 30))

	sm.SetSubagentStatus(key, "needs_context")

	sm.mu.RLock()
	sess := sm.sessions[key]
	meta := sm.sessionMeta[key]
	sm.mu.RUnlock()

	if sess == nil || sess.SubagentStatus != "needs_context" {
		t.Fatalf("session SubagentStatus = %q, want needs_context", sessStatus(sess))
	}
	if meta == nil || meta.SubagentStatus != "needs_context" {
		t.Fatalf("meta SubagentStatus = %v, want needs_context", meta)
	}
	if meta != nil && !meta.Updated.After(time.Now().Add(-time.Minute)) {
		t.Errorf("meta.Updated should be refreshed, got %v", meta.Updated)
	}
}

func sessStatus(s *Session) string {
	if s == nil {
		return "<nil session>"
	}
	return s.SubagentStatus
}

func TestSetSubagentStatus_DoesNotClobberMessageContent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:subagent-11"
	sm.AddMessage(key, "user", "probe")
	sm.AddMessage(key, "assistant", "the answer is 42")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sm.SetSubagentStatus(key, "cancelled")

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	hist := sm2.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("history after status write = %d messages, want 2", len(hist))
	}
	if hist[0].Role != "user" || hist[0].Content != "probe" {
		t.Fatalf("first message changed: %+v", hist[0])
	}
	if hist[1].Content != "the answer is 42" {
		t.Fatalf("second message changed: %+v", hist[1])
	}
}
