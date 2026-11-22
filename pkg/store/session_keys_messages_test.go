package store

import (
	"path/filepath"
	"testing"
)

func newKeysRepo(t *testing.T) *SessionRepo {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.Sessions()
}

func TestSessionKeysWithMessages(t *testing.T) {
	repo := newKeysRepo(t)

	if err := repo.UpsertSession(SessionMeta{Key: "a", Mode: "agent"}); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if err := repo.UpsertSession(SessionMeta{Key: "b", Mode: "agent"}); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if err := repo.UpsertSession(SessionMeta{Key: "c", Mode: "agent"}); err != nil {
		t.Fatalf("upsert c: %v", err)
	}
	if err := repo.InsertMessage("a", 0, "user", `{"role":"user","content":"hi"}`, false); err != nil {
		t.Fatalf("insert a/0: %v", err)
	}
	if err := repo.InsertMessage("a", 1, "assistant", `{"role":"assistant","content":"yo"}`, false); err != nil {
		t.Fatalf("insert a/1: %v", err)
	}
	if err := repo.InsertMessage("c", 0, "user", `{"role":"user","content":"bye"}`, false); err != nil {
		t.Fatalf("insert c/0: %v", err)
	}
	if err := repo.DeleteSession("c"); err != nil {
		t.Fatalf("delete c: %v", err)
	}

	got, err := repo.SessionKeysWithMessages()
	if err != nil {
		t.Fatalf("SessionKeysWithMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("result = %v, want exactly {a}", got)
	}
	if !got["a"] {
		t.Errorf(`got["a"] = false, want true`)
	}
	if got["b"] {
		t.Errorf(`got["b"] = true, want false (session row without messages)`)
	}

	for _, k := range []string{"a", "b"} {
		n, err := repo.MessageCount(k)
		if err != nil {
			t.Fatalf("message count %s: %v", k, err)
		}
		if got[k] != (n > 0) {
			t.Errorf("key %q: batch=%v count=%d, mismatch", k, got[k], n)
		}
	}

	empty := newKeysRepo(t)
	none, err := empty.SessionKeysWithMessages()
	if err != nil {
		t.Fatalf("SessionKeysWithMessages on empty db: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Errorf("empty db result = %v, want empty non-nil map", none)
	}
}
