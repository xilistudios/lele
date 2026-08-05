package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
)

func openGoalStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGoalManager_SetStore_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	gm.Set("session1", "test goal", 5)

	// Verify goal was persisted in the repo.
	goalJSON, found, err := repo.GetGoal("session1")
	if err != nil {
		t.Fatalf("GetGoal error: %v", err)
	}
	if !found {
		t.Fatal("goal not persisted in repo")
	}
	if goalJSON == "" {
		t.Fatal("goal JSON is empty")
	}

	// Create a new manager, load from store, and verify.
	gm2 := NewGoalManager(dir)
	gm2.SetStore(repo)

	goal := gm2.Get("session1")
	if goal == nil {
		t.Fatal("goal not loaded from store")
	}
	if goal.Text != "test goal" {
		t.Errorf("Text = %q, want %q", goal.Text, "test goal")
	}
	if goal.MaxTurns != 5 {
		t.Errorf("MaxTurns = %d, want 5", goal.MaxTurns)
	}
	if goal.Status != GoalActive {
		t.Errorf("Status = %q, want %q", goal.Status, GoalActive)
	}
	if goal.SessionKey != "session1" {
		t.Errorf("SessionKey = %q, want %q", goal.SessionKey, "session1")
	}
}

func TestGoalManager_SetStore_LazyMigration(t *testing.T) {
	dir := t.TempDir()

	// Create a goal using the JSON backend (no store).
	gm1 := NewGoalManager(dir)
	gm1.Set("legacy-session", "legacy goal", 7)

	// Verify the legacy JSON file exists.
	legacyPath := filepath.Join(dir, "legacy-session.json")
	if _, err := os.Stat(legacyPath); os.IsNotExist(err) {
		t.Fatal("expected legacy JSON file to exist")
	}

	// Now switch to SQLite. The legacy goal should be migrated.
	s := openGoalStore(t)
	repo := s.Goals()

	gm2 := NewGoalManager(dir)
	gm2.SetStore(repo)

	// Verify the goal was migrated into the repo.
	_, found, err := repo.GetGoal("legacy-session")
	if err != nil {
		t.Fatalf("GetGoal error: %v", err)
	}
	if !found {
		t.Fatal("legacy goal not migrated to store")
	}

	// Verify the goal is accessible from the new manager.
	goal := gm2.Get("legacy-session")
	if goal == nil {
		t.Fatal("legacy goal not loaded into manager")
	}
	if goal.Text != "legacy goal" {
		t.Errorf("Text = %q, want %q", goal.Text, "legacy goal")
	}
	if goal.MaxTurns != 7 {
		t.Errorf("MaxTurns = %d, want 7", goal.MaxTurns)
	}
}

func TestGoalManager_Clear_DeletesFromRepo(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	gm.Set("session1", "to be cleared", 5)

	// Verify it's in the repo.
	_, found, err := repo.GetGoal("session1")
	if err != nil {
		t.Fatalf("GetGoal error: %v", err)
	}
	if !found {
		t.Fatal("goal should exist in repo before clear")
	}

	if !gm.Clear("session1") {
		t.Fatal("Clear returned false")
	}

	// Verify it was deleted from the repo.
	_, found, err = repo.GetGoal("session1")
	if err != nil {
		t.Fatalf("GetGoal error after clear: %v", err)
	}
	if found {
		t.Fatal("goal should be deleted from repo after clear")
	}
}
