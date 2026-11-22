package session

import (
	"testing"
)

func evictAll(sm *SessionManager) {
	sm.mu.Lock()
	for k := range sm.sessions {
		delete(sm.sessions, k)
		delete(sm.accessTimes, k)
	}
	sm.mu.Unlock()
}

func indexByKey(entries []SessionIndexEntry) map[string]SessionIndexEntry {
	out := make(map[string]SessionIndexEntry, len(entries))
	for _, e := range entries {
		if _, dup := out[e.Key]; dup {
			panic("duplicate key in session index: " + e.Key)
		}
		out[e.Key] = e
	}
	return out
}

func TestListSessionIndex_ResidentSessions(t *testing.T) {
	sm := NewSessionManager()

	sm.GetOrCreate("resident-a")
	sm.AddMessage("resident-a", "user", "hello")
	sm.AddMessage("resident-a", "assistant", "hi")
	if err := sm.SetName("resident-a", "Greeting"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := sm.SetMode("resident-a", "chat"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := sm.SetFolder("resident-a", "/tmp/folder"); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}

	sm.GetOrCreate("resident-empty")

	entries := sm.ListSessionIndex()
	if len(entries) != 2 {
		t.Fatalf("index size = %d, want 2 (%+v)", len(entries), entries)
	}
	byKey := indexByKey(entries)

	got := byKey["resident-a"]
	if got.Key != "resident-a" || got.Name != "Greeting" || got.Mode != "chat" || got.Folder != "/tmp/folder" {
		t.Fatalf("resident-a fields = %+v, want name/mode/folder from setters", got)
	}
	if !got.Resident {
		t.Errorf("resident-a Resident = false, want true")
	}
	if !got.HasMessages {
		t.Errorf("resident-a HasMessages = false, want true")
	}
	if got.Created.IsZero() || got.Updated.IsZero() {
		t.Errorf("resident-a timestamps zero: %+v", got)
	}

	empty := byKey["resident-empty"]
	if !empty.Resident {
		t.Errorf("resident-empty Resident = false, want true")
	}
	if empty.HasMessages {
		t.Errorf("resident-empty HasMessages = true, want false (no messages)")
	}
}

func TestListSessionIndex_MatchesGetters(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	keys := []string{"meta:a", "meta:b", "meta:empty"}
	for i, k := range keys {
		sm.GetOrCreate(k)
		sm.SetName(k, "name-"+k)
		sm.SetMode(k, "agent")
		sm.SetFolder(k, "/folder/"+k)
		if i < 2 {
			sm.AddMessage(k, "user", "msg")
		}
		if err := sm.Save(k); err != nil {
			t.Fatalf("save %s: %v", k, err)
		}
	}

	evictAll(sm)

	entries := sm.ListSessionIndex()
	if len(entries) != len(keys) {
		t.Fatalf("index size = %d, want %d", len(entries), len(keys))
	}
	byKey := indexByKey(entries)

	for _, k := range keys {
		e, ok := byKey[k]
		if !ok {
			t.Fatalf("session %q missing from index", k)
		}
		if e.Resident {
			t.Errorf("%s Resident = true, want false after eviction", k)
		}
		if want := sm.GetName(k); e.Name != want {
			t.Errorf("%s Name = %q, want %q (GetName)", k, e.Name, want)
		}
		if want := sm.GetMode(k); e.Mode != want {
			t.Errorf("%s Mode = %q, want %q (GetMode)", k, e.Mode, want)
		}
		if want := sm.GetFolder(k); e.Folder != want {
			t.Errorf("%s Folder = %q, want %q (GetFolder)", k, e.Folder, want)
		}
		if want := sm.GetCreated(k); !e.Created.Equal(want) {
			t.Errorf("%s Created = %v, want %v", k, e.Created, want)
		}
		if want := sm.GetUpdated(k); !e.Updated.Equal(want) {
			t.Errorf("%s Updated = %v, want %v", k, e.Updated, want)
		}
	}
}

func TestSessionKeysWithMessages_MatchesHasMessages(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	sm.GetOrCreate("cold:with")
	sm.AddMessage("cold:with", "user", "hello")
	if err := sm.Save("cold:with"); err != nil {
		t.Fatalf("save: %v", err)
	}
	sm.GetOrCreate("cold:empty")
	if err := sm.Save("cold:empty"); err != nil {
		t.Fatalf("save: %v", err)
	}

	evictAll(sm)

	byKey := indexByKey(sm.ListSessionIndex())
	for _, k := range []string{"cold:with", "cold:empty"} {
		if byKey[k].HasMessages {
			t.Errorf("%s index HasMessages = true, want false (non-resident, unresolved)", k)
		}
	}

	withMessages := sm.SessionKeysWithMessages()
	if withMessages == nil {
		t.Fatalf("SessionKeysWithMessages() = nil with a store configured")
	}
	for _, k := range []string{"cold:with", "cold:empty"} {
		want := sm.HasMessages(k)
		if got := withMessages[k]; got != want {
			t.Errorf("SessionKeysWithMessages()[%q] = %v, want %v (HasMessages)", k, got, want)
		}
	}
}

func TestSessionKeysWithMessages_NoStore(t *testing.T) {
	sm := NewSessionManager()
	sm.GetOrCreate("memory:only")
	sm.AddMessage("memory:only", "user", "hi")

	if got := sm.SessionKeysWithMessages(); got != nil {
		t.Fatalf("SessionKeysWithMessages() = %v, want nil without a store", got)
	}
	byKey := indexByKey(sm.ListSessionIndex())
	if !byKey["memory:only"].HasMessages {
		t.Errorf("memory:only HasMessages = false, want true (resident)")
	}
}
