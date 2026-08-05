package group

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func openSQLiteStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close() failed: %v", err)
		}
	})
	return s
}

func useTestGroupRepo(t *testing.T, repo *store.GroupRepo) {
	t.Helper()
	UseStore(repo)
	t.Cleanup(func() { UseStore(nil) })
}

func TestUseStore_SaveLoadRoundTrip(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())
	dir := t.TempDir()

	state := &GroupState{
		ID:        "group:sql/roundtrip",
		ProfileID: "default",
		Task:      "solve it",
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleAggregator, Label: "B"},
		},
		Strategy:  "round_robin",
		Status:    StatusDone,
		CreatedAt: time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 3, 1, 10, 5, 0, 0, time.UTC),
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Label: "A", Content: "hello", CreatedAt: time.Date(2025, 3, 1, 10, 1, 0, 0, time.UTC), Tokens: 50},
			{Index: 1, Layer: 0, Speaker: "b", Label: "B", Content: "world", CreatedAt: time.Date(2025, 3, 1, 10, 2, 0, 0, time.UTC), Tokens: 100},
		},
		TotalTokens:   150,
		Rounds:        1,
		MaxTurns:      2,
		OriginChannel: "telegram",
		OriginChatID:  "123",
	}

	if err := SaveGroup(dir, state); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	loaded, err := LoadGroup(dir, state.ID)
	if err != nil {
		t.Fatalf("LoadGroup: %v", err)
	}

	if !reflect.DeepEqual(loaded, state) {
		t.Errorf("loaded state does not match original\ngot:  %+v\nwant: %+v", loaded, state)
	}

	// Verify no legacy JSON file was written.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no files in dir, got %d", len(entries))
	}
}

func TestUseStore_LazyMigration(t *testing.T) {
	// Start with legacy backend.
	UseStore(nil)
	dir := t.TempDir()

	state := &GroupState{
		ID:        "group:migrate",
		Task:      "migrate me",
		Status:    StatusDone,
		CreatedAt: time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 3, 1, 10, 5, 0, 0, time.UTC),
	}

	// Save via legacy.
	if err := SaveGroup(dir, state); err != nil {
		t.Fatalf("SaveGroup legacy: %v", err)
	}

	// Verify the legacy file exists.
	legacyFile := filepath.Join(dir, sanitizeGroupID(state.ID)+".json")
	if _, err := os.Stat(legacyFile); err != nil {
		t.Fatalf("legacy JSON file should exist: %v", err)
	}

	// Switch to SQLite.
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	// LoadGroup should trigger lazy migration.
	loaded, err := LoadGroup(dir, state.ID)
	if err != nil {
		t.Fatalf("LoadGroup: %v", err)
	}
	if loaded.ID != state.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, state.ID)
	}

	// Verify migration inserted into the repo.
	m, err := s.Groups().ListGroupStates()
	if err != nil {
		t.Fatalf("ListGroupStates: %v", err)
	}
	if len(m) != 1 {
		t.Errorf("repo has %d entries, want 1", len(m))
	}
	if m[state.ID] == "" {
		t.Errorf("repo entry for %s is empty", state.ID)
	}
}

func TestUseStore_ListGroups(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	now := time.Date(2025, 3, 1, 12, 0, 0, 0, time.UTC)

	s1 := &GroupState{
		ID:        "group-new",
		Task:      "new task",
		Status:    StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s2 := &GroupState{
		ID:        "group-old",
		Task:      "old task",
		Status:    StatusDone,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	}

	// dir is ignored for SQLite backend.
	dir := t.TempDir()
	if err := SaveGroup(dir, s1); err != nil {
		t.Fatalf("SaveGroup s1: %v", err)
	}
	if err := SaveGroup(dir, s2); err != nil {
		t.Fatalf("SaveGroup s2: %v", err)
	}

	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListGroups returned %d, want 2", len(list))
	}
	if list[0].ID != "group-new" {
		t.Errorf("list[0].ID = %q, want %q (should be newest first)", list[0].ID, "group-new")
	}
	if list[1].ID != "group-old" {
		t.Errorf("list[1].ID = %q, want %q", list[1].ID, "group-old")
	}
}

func TestUseStore_NilFallback(t *testing.T) {
	// Explicitly ensure no SQLite backend.
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	dir := t.TempDir()

	state := &GroupState{
		ID:        "group:fallback",
		Task:      "fallback task",
		Status:    StatusDone,
		CreatedAt: time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 3, 1, 10, 5, 0, 0, time.UTC),
	}

	if err := SaveGroup(dir, state); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	// Verify the JSON file exists.
	expectedFile := filepath.Join(dir, sanitizeGroupID(state.ID)+".json")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("legacy JSON file should exist: %v", err)
	}

	// Load and verify.
	loaded, err := LoadGroup(dir, state.ID)
	if err != nil {
		t.Fatalf("LoadGroup: %v", err)
	}
	if loaded.ID != state.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, state.ID)
	}

	// ListGroups should return 1.
	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListGroups returned %d, want 1", len(list))
	}
}
