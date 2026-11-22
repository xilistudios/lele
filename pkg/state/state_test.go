package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
)

func TestAtomicSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	err = sm.SetLastChannel("test-channel")
	if err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	lastChannel := sm.GetLastChannel()
	if lastChannel != "test-channel" {
		t.Errorf("Expected channel 'test-channel', got '%s'", lastChannel)
	}

	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}

	stateFile := filepath.Join(tmpDir, "state", "state.json")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Error("Expected state file to exist")
	}

	sm2 := NewManager(tmpDir)
	if sm2.GetLastChannel() != "test-channel" {
		t.Errorf("Expected persistent channel 'test-channel', got '%s'", sm2.GetLastChannel())
	}
}

func TestSetLastChatID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	err = sm.SetLastChatID("test-chat-id")
	if err != nil {
		t.Fatalf("SetLastChatID failed: %v", err)
	}

	lastChatID := sm.GetLastChatID()
	if lastChatID != "test-chat-id" {
		t.Errorf("Expected chat ID 'test-chat-id', got '%s'", lastChatID)
	}

	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}

	sm2 := NewManager(tmpDir)
	if sm2.GetLastChatID() != "test-chat-id" {
		t.Errorf("Expected persistent chat ID 'test-chat-id', got '%s'", sm2.GetLastChatID())
	}
}

func TestAtomicity_NoCorruptionOnInterrupt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	err = sm.SetLastChannel("initial-channel")
	if err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	tempFile := filepath.Join(tmpDir, "state", "state.json.tmp")
	err = os.WriteFile(tempFile, []byte("corrupted data"), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	lastChannel := sm.GetLastChannel()
	if lastChannel != "initial-channel" {
		t.Errorf("Expected channel 'initial-channel' after corrupted temp file, got '%s'", lastChannel)
	}

	os.Remove(tempFile)

	err = sm.SetLastChannel("new-channel")
	if err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	if sm.GetLastChannel() != "new-channel" {
		t.Errorf("Expected channel 'new-channel', got '%s'", sm.GetLastChannel())
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			channel := fmt.Sprintf("channel-%d", idx)
			sm.SetLastChannel(channel)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	lastChannel := sm.GetLastChannel()
	if lastChannel == "" {
		t.Error("Expected non-empty channel after concurrent writes")
	}

	stateFile := filepath.Join(tmpDir, "state", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("Failed to read state file: %v", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Errorf("State file contains invalid JSON: %v", err)
	}
}

func TestNewManager_ExistingState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm1 := NewManager(tmpDir)
	sm1.SetLastChannel("existing-channel")
	sm1.SetLastChatID("existing-chat-id")

	sm2 := NewManager(tmpDir)

	if sm2.GetLastChannel() != "existing-channel" {
		t.Errorf("Expected channel 'existing-channel', got '%s'", sm2.GetLastChannel())
	}

	if sm2.GetLastChatID() != "existing-chat-id" {
		t.Errorf("Expected chat ID 'existing-chat-id', got '%s'", sm2.GetLastChatID())
	}
}

func TestNewManager_EmptyWorkspace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	if sm.GetLastChannel() != "" {
		t.Errorf("Expected empty channel, got '%s'", sm.GetLastChannel())
	}

	if sm.GetLastChatID() != "" {
		t.Errorf("Expected empty chat ID, got '%s'", sm.GetLastChatID())
	}

	if !sm.GetTimestamp().IsZero() {
		t.Error("Expected zero timestamp for new state")
	}
}

func TestKVRepoPersistence(t *testing.T) {
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

	sm := NewManager(tmpDir)
	sm.SetKVRepo(s.KV())

	if err := sm.SetLastChannel("kv-channel"); err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}
	if err := sm.SetLastChatID("kv-chat-id"); err != nil {
		t.Fatalf("SetLastChatID failed: %v", err)
	}

	key := "state:" + tmpDir
	val, ok, err := s.KV().Get(key)
	if err != nil || !ok {
		t.Fatalf("KV Get(%q) = (_, %v, %v), want (_, true, nil)", key, ok, err)
	}
	var persisted State
	if err := json.Unmarshal([]byte(val), &persisted); err != nil {
		t.Fatalf("KV value is invalid JSON: %v", err)
	}
	if persisted.LastChannel != "kv-channel" || persisted.LastChatID != "kv-chat-id" {
		t.Errorf("KV persisted state = %+v, want channel 'kv-channel', chat ID 'kv-chat-id'", persisted)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "state", "state.json")); !os.IsNotExist(err) {
		t.Error("Expected no state.json file when KV repo is wired in")
	}

	sm2 := NewManager(tmpDir)
	sm2.SetKVRepo(s.KV())
	if sm2.GetLastChannel() != "kv-channel" {
		t.Errorf("Expected loaded channel 'kv-channel', got '%s'", sm2.GetLastChannel())
	}
	if sm2.GetLastChatID() != "kv-chat-id" {
		t.Errorf("Expected loaded chat ID 'kv-chat-id', got '%s'", sm2.GetLastChatID())
	}
}

func TestKVRepoFallbackToFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)
	if err := sm.SetLastChannel("file-channel"); err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	sm.SetKVRepo(s.KV())

	if err := sm.SetLastChatID("kv-chat-id"); err != nil {
		t.Fatalf("SetLastChatID failed: %v", err)
	}

	key := "state:" + tmpDir
	val, ok, err := s.KV().Get(key)
	if err != nil || !ok {
		t.Fatalf("KV Get(%q) = (_, %v, %v), want (_, true, nil)", key, ok, err)
	}
	var persisted State
	if err := json.Unmarshal([]byte(val), &persisted); err != nil {
		t.Fatalf("KV value is invalid JSON: %v", err)
	}
	if persisted.LastChannel != "file-channel" {
		t.Errorf("Expected channel 'file-channel' carried into KV, got '%s'", persisted.LastChannel)
	}
	if persisted.LastChatID != "kv-chat-id" {
		t.Errorf("Expected chat ID 'kv-chat-id' in KV, got '%s'", persisted.LastChatID)
	}
}
