package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
)

// TestNewManager_MissingStateDir exercises the MkdirAll failure path where
// the workspace path is actually a regular file, so creating a state
// subdirectory underneath it fails.
func TestNewManager_MkdirAllFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a regular file at workspace path so filepath.Join(...)/state
	// cannot be created.
	workspaceFile := filepath.Join(tmpDir, "not-a-dir")
	if err := os.WriteFile(workspaceFile, []byte("x"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	sm := NewManager(workspaceFile)
	if sm != nil {
		t.Fatal("Expected NewManager to return nil when MkdirAll fails")
	}
}

// TestNewManager_MigrateFromOldLocation verifies that state stored at the
// legacy workspace/state.json location is migrated into the new
// workspace/state/state.json file.
func TestNewManager_MigrateFromOldLocation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write an old-format state file in the old location.
	oldPath := filepath.Join(tmpDir, "state.json")
	old := State{LastChannel: "migrated-channel", LastChatID: "migrated-chat"}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("Failed to marshal old state: %v", err)
	}
	if err := os.WriteFile(oldPath, data, 0644); err != nil {
		t.Fatalf("Failed to write old state: %v", err)
	}

	sm := NewManager(tmpDir)
	if sm == nil {
		t.Fatal("NewManager returned nil")
	}

	if sm.GetLastChannel() != "migrated-channel" {
		t.Errorf("Expected migrated channel 'migrated-channel', got '%s'", sm.GetLastChannel())
	}
	if sm.GetLastChatID() != "migrated-chat" {
		t.Errorf("Expected migrated chat ID 'migrated-chat', got '%s'", sm.GetLastChatID())
	}

	// The legacy file should have been migrated: new location exists, old
	// location state should now be under the new directory.
	newPath := filepath.Join(tmpDir, "state", "state.json")
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("Expected new state file to exist after migration")
	}

	// Now a fresh manager should load from the new location.
	sm2 := NewManager(tmpDir)
	if sm2 == nil {
		t.Fatal("NewManager returned nil on reload")
	}
	if sm2.GetLastChannel() != "migrated-channel" {
		t.Errorf("Persisted channel after reload = '%s', want 'migrated-channel'", sm2.GetLastChannel())
	}
}

// TestNewManager_LoadNewLocationFailure verifies that an unreadable/invalid
// new-location state file causes NewManager to log an error but still
// return a usable manager with an empty in-memory state.
func TestNewManager_LoadNewLocationFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatalf("Failed to create state dir: %v", err)
	}
	// Invalid JSON in the new location.
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("Failed to write invalid state file: %v", err)
	}

	sm := NewManager(tmpDir)
	if sm == nil {
		t.Fatal("NewManager returned nil")
	}
	if sm.GetLastChannel() != "" {
		t.Errorf("Expected empty state after load failure, got '%s'", sm.GetLastChannel())
	}
}

// TestNewManager_MigrationWithUnreadableOldFile ensures the manager still
// works (empty state) when the legacy file exists but cannot be parsed.
func TestNewManager_MigrationUnreadableOldFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldPath := filepath.Join(tmpDir, "state.json")
	if err := os.WriteFile(oldPath, []byte("{corrupt"), 0644); err != nil {
		t.Fatalf("Failed to write corrupt old file: %v", err)
	}

	sm := NewManager(tmpDir)
	if sm == nil {
		t.Fatal("NewManager returned nil")
	}
	if sm.GetLastChannel() != "" {
		t.Errorf("Expected empty state, got '%s'", sm.GetLastChannel())
	}
}

// TestSetKVRepo_Nil restores the JSON file fallback when nil is passed and
// insists on writing to disk afterwards.
func TestSetKVRepo_Nil(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)
	if sm == nil {
		t.Fatal("NewManager returned nil")
	}
	sm.SetKVRepo(nil)
	if err := sm.SetLastChannel("fallback-channel"); err != nil {
		t.Fatalf("SetLastChannel with nil KV failed: %v", err)
	}

	// Must have gone to disk.
	st, err := os.Stat(filepath.Join(tmpDir, "state", "state.json"))
	if err != nil {
		t.Fatalf("Expected state.json when KV is nil: %v", err)
	}
	if st.Size() == 0 {
		t.Error("Expected non-empty state.json")
	}
}

// TestSetKVRepo_LoadErrorFromClosedDB verifies the error-logging path in
// SetKVRepo when the KV store cannot be read (closed DB).
func TestSetKVRepo_LoadError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	kv := s.KV()
	// Close the DB so reads fail.
	if err := s.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	sm := NewManager(tmpDir)
	sm.SetKVRepo(kv) // should log error, not crash
	if sm == nil {
		t.Fatal("NewManager returned nil")
	}
}

// TestSetKVRepo_UnmarshalError seeds invalid JSON into the KV store under
// the state key and verifies SetKVRepo keeps the current in-memory state.
func TestSetKVRepo_UnmarshalError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	key := "state:" + tmpDir
	if err := s.KV().Set(key, "{invalid json"); err != nil {
		t.Fatalf("Failed to seed bad KV value: %v", err)
	}

	sm := NewManager(tmpDir)
	sm.SetLastChannel("mem-channel") // in-memory value
	sm.SetKVRepo(s.KV())             // unmarshal of KV value fails -> keeps in-memory
	if sm.GetLastChannel() != "mem-channel" {
		t.Errorf("Expected in-memory channel preserved after unmarshal failure, got '%s'", sm.GetLastChannel())
	}
}

// TestSetLastChannel_SaveKVError verifies the error path when the KV store
// is wired in but the write fails.
func TestSetLastChannel_SaveKVError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	kv := s.KV()
	if err := s.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	sm := NewManager(tmpDir)
	sm.SetKVRepo(kv)

	if err := sm.SetLastChannel("x"); err == nil {
		t.Error("Expected error when saving to closed KV store")
	}
}

// TestSetLastChatID_SaveKVError mirrors the KV write failure for chat IDs.
func TestSetLastChatID_SaveKVError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	kv := s.KV()
	if err := s.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	sm := NewManager(tmpDir)
	sm.SetKVRepo(kv)

	if err := sm.SetLastChatID("x"); err == nil {
		t.Error("Expected error when saving to closed KV store")
	}
}

// TestSaveAtomic_WriteError verifies the temp-file write failure path by
// pointing the state file into a non-existent directory.
func TestSaveAtomic_WriteError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	missingDir := filepath.Join(tmpDir, "does-not-exist")
	sm := &Manager{
		workspace: tmpDir,
		stateFile: filepath.Join(missingDir, "state.json"),
		state:     &State{LastChannel: "c"},
	}

	if err := sm.saveAtomic(); err == nil {
		t.Error("Expected write error for missing parent directory")
	}
}

// TestSaveAtomic_RenameError verifies the rename failure + temp cleanup
// path by making the target an existing directory.
func TestSaveAtomic_RenameError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Target is an existing directory, so rename(temppath, target) fails.
	targetDir := filepath.Join(tmpDir, "target-dir")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("Failed to create target dir: %v", err)
	}

	sm := &Manager{
		workspace: tmpDir,
		stateFile: targetDir, // stateFile is a directory
		state:     &State{LastChannel: "c"},
	}

	if err := sm.saveAtomic(); err == nil {
		t.Error("Expected rename failure when target is a directory")
	}

	// Temp file must have been cleaned up.
	if _, err := os.Stat(targetDir + ".tmp"); !os.IsNotExist(err) {
		t.Error("Expected temp file to be cleaned up after failed rename")
	}
}

// TestLoad_KVGetError verifies load's error path when KV read fails.
func TestLoad_KVGetError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	kv := s.KV()
	if err := s.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}

	sm := &Manager{workspace: tmpDir, kvRepo: kv, state: &State{}}
	if err := sm.load(); err == nil {
		t.Error("Expected error loading from closed KV store")
	}
}

// TestLoad_KVMissingKey verifies load returns nil when the KV key does not
// exist yet (OK condition).
func TestLoad_KVMissingKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	sm := &Manager{workspace: tmpDir, kvRepo: s.KV(), state: &State{}}
	if err := sm.load(); err != nil {
		t.Errorf("Expected no error for missing KV key, got: %v", err)
	}
}

// TestLoad_KVUnmarshalError verifies load fails to parse corrupted KV data.
func TestLoad_KVUnmarshalError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	if err := s.KV().Set("state:"+tmpDir, "{bad"); err != nil {
		t.Fatalf("Failed to seed bad KV value: %v", err)
	}

	sm := &Manager{workspace: tmpDir, kvRepo: s.KV(), state: &State{}}
	if err := sm.load(); err == nil {
		t.Error("Expected unmarshal error from corrupted KV value")
	}
}

// TestLoad_FileMissing verifies load returns nil when the file does not
// exist yet.
func TestLoad_FileMissing(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := &Manager{
		workspace: tmpDir,
		stateFile: filepath.Join(tmpDir, "state", "state.json"),
		state:     &State{},
	}
	if err := sm.load(); err != nil {
		t.Errorf("Expected nil for missing file, got: %v", err)
	}
}

// TestLoad_FileReadError verifies load returns an error when reading the
// state file fails for a reason other than not-exist (e.g. it is a
// directory).
func TestLoad_FileReadError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dirPath := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}

	sm := &Manager{workspace: tmpDir, stateFile: dirPath, state: &State{}}
	if err := sm.load(); err == nil {
		t.Error("Expected read error when state file is a directory")
	}
}

// TestLoad_FileUnmarshalError verifies load fails on a corrupt state file.
func TestLoad_FileUnmarshalError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sp := filepath.Join(tmpDir, "state", "state.json")
	if err := os.MkdirAll(filepath.Dir(sp), 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	if err := os.WriteFile(sp, []byte("{bad"), 0644); err != nil {
		t.Fatalf("Failed to write corrupt file: %v", err)
	}

	sm := &Manager{workspace: tmpDir, stateFile: sp, state: &State{}}
	if err := sm.load(); err == nil {
		t.Error("Expected unmarshal error from corrupt state file")
	}
}

// TestLoad_FileValid loads a valid state from disk.
func TestLoad_FileValid(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sp := filepath.Join(tmpDir, "state", "state.json")
	if err := os.MkdirAll(filepath.Dir(sp), 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}
	good := State{LastChannel: "loaded", LastChatID: "the-id"}
	data, err := json.Marshal(good)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	if err := os.WriteFile(sp, data, 0644); err != nil {
		t.Fatalf("Failed to write: %v", err)
	}

	sm := &Manager{workspace: tmpDir, stateFile: sp, state: &State{}}
	if err := sm.load(); err != nil {
		t.Fatalf("load returned error: %v", err)
	}
	if sm.GetLastChannel() != "loaded" || sm.GetLastChatID() != "the-id" {
		t.Errorf("Failed to load state: channel=%q chat=%q", sm.GetLastChannel(), sm.GetLastChatID())
	}
}

// TestGetTimestamp_AfterUpdate confirms timestamps round-trip through the
// setters.
func TestGetTimestamp_AfterUpdate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)
	before := sm.GetTimestamp()
	if err := sm.SetLastChannel("c"); err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}
	after := sm.GetTimestamp()
	if after.Before(before) {
		t.Error("Expected timestamp to advance after update")
	}
}
