package session

import "testing"

func TestSessionManager_Folder_RoundTripAndPersistence(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:folder"
	sm.GetOrCreate(key)

	if got := sm.GetFolder(key); got != "" {
		t.Errorf("GetFolder before set = %q, want \"\"", got)
	}

	if err := sm.SetFolder(key, "/home/user/projects/demo"); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if got := sm.GetFolder(key); got != "/home/user/projects/demo" {
		t.Errorf("GetFolder = %q, want %q", got, "/home/user/projects/demo")
	}
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	if got := sm2.GetFolder(key); got != "/home/user/projects/demo" {
		t.Errorf("GetFolder after reload = %q, want %q", got, "/home/user/projects/demo")
	}

	if err := sm2.SetFolder(key, ""); err != nil {
		t.Fatalf("SetFolder clear: %v", err)
	}
	if got := sm2.GetFolder(key); got != "" {
		t.Errorf("GetFolder after clear = %q, want \"\"", got)
	}
	sm3 := NewSessionManager()
	sm3.SetSessionRepo(s.Sessions())
	if got := sm3.GetFolder(key); got != "" {
		t.Errorf("GetFolder after clear + reload = %q, want \"\"", got)
	}
}

func TestSessionManager_Folder_LightweightMetaRead(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:meta-read"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	if err := sm.SetFolder(key, "/tmp/whatever"); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	if got := sm2.GetFolder(key); got != "/tmp/whatever" {
		t.Fatalf("GetFolder cold = %q, want %q", got, "/tmp/whatever")
	}
	sm2.mu.RLock()
	_, resident := sm2.sessions[key]
	sm2.mu.RUnlock()
	if resident {
		t.Error("GetFolder must not load the full session into memory (N+1 regression)")
	}
}

func TestSessionManager_Folder_SetOnUnknownSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:ghost"
	if err := sm.SetFolder(key, "/tmp/ghost"); err != nil {
		t.Fatalf("SetFolder on unknown session: %v", err)
	}
	if got := sm.GetFolder(key); got != "/tmp/ghost" {
		t.Errorf("GetFolder = %q, want %q", got, "/tmp/ghost")
	}
}

func TestSessionManager_Folder_SetNameKeepsFolder(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "native:client-1:name"
	sm.GetOrCreate(key)
	if err := sm.SetFolder(key, "/tmp/keepme"); err != nil {
		t.Fatalf("SetFolder: %v", err)
	}
	if err := sm.SetName(key, "renamed"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if got := sm.GetFolder(key); got != "/tmp/keepme" {
		t.Errorf("GetFolder after SetName = %q, want %q", got, "/tmp/keepme")
	}
}
