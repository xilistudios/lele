// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Package v5 coverage: goal persistence error branches (via a closed SQLite
// repo) and command-dispatcher fall-through paths.

package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// TestGoalManager_SetStore_ClosedRepo drives the SetGoal failure branch in
// persist() by closing the underlying DB so repo.SetGoal returns an error.
func TestGoalManager_SetStore_ClosedRepo(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)

	// Close the store so further repo ops fail.
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Setting a goal must not panic; persist() hits the SetGoal error branch.
	gm.Set("closed-session", "do things", 5)
	if gm.Get("closed-session") == nil {
		t.Error("goal should still be in memory after persist error")
	}
}

// TestGoalManager_loadFromRepo_MigrationSetGoalError drives the loadFromRepo
// migration SetGoal error branch: legacy JSON file exists on disk, repo is
// empty, but the DB is closed so migration SetGoal fails.
func TestGoalManager_loadFromRepo_MigrationSetGoalError(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	// Seed a legacy JSON goal on disk via the JSON backend first.
	gm1 := NewGoalManager(dir)
	gm1.Set("legacy-mig", "legacy goal", 3)

	// New manager whose loadFromDisk() reads the legacy file, then SetStore
	// migrates (repo empty). Close the DB so the migration SetGoal path fails.
	gm2 := NewGoalManager(dir)
	gm2.SetStore(repo)
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	// loadFromRepo with the repo already migrated on SetStore has zero goals;
	// the migration SetGoal error is hit via persist path. Call directly to
	// exercise the repo-empty migrate branch with a closed DB.
	gm2.loadFromRepo()
	if gm2.Get("legacy-mig") == nil {
		t.Error("legacy goal should be in memory")
	}
}

// TestGoalManager_loadFromRepo_CorruptEntry drives the corrupt-goal skip branch
// in loadFromRepo by storing invalid JSON in the repo directly.
func TestGoalManager_loadFromRepo_CorruptEntry(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	if err := repo.SetGoal("corrupt", "{not valid json"); err != nil {
		t.Fatalf("seed corrupt goal: %v", err)
	}

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	if gm.Get("corrupt") != nil {
		t.Error("corrupt goal should not be loaded")
	}
}

// TestGoalManager_loadFromRepo_ListError drives the ListGoals error branch by
// closing the DB before loadFromRepo.
func TestGoalManager_loadFromRepo_ListError(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	gm.loadFromRepo() // must not panic
}

// TestGoalManager_loadFromRepo_RestoresActivePaused drives the restore of both
// active and paused goals from the repo and the skip of done goals.
func TestGoalManager_loadFromRepo_RestoresActivePaused(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm1 := NewGoalManager(dir)
	gm1.SetStore(repo)
	gm1.Set("active-s", "active goal", 4)
	gm1.Set("paused-s", "paused goal", 4)
	gm1.Pause("paused-s")
	gm1.Set("done-s", "done goal", 4)
	gm1.MarkDone("done-s")

	// Reload from repo into a fresh manager.
	gm2 := NewGoalManager(dir)
	gm2.SetStore(repo)
	if gm2.Get("active-s") == nil {
		t.Error("active goal should be restored")
	}
	if gm2.Get("paused-s") == nil {
		t.Error("paused goal should be restored")
	}
	if gm2.Get("done-s") != nil {
		t.Error("done goal should not be restored")
	}
}

// TestCommandHandler_NonCommandNotHandled covers the top-level dispatcher's
// early return for non-command content.
func TestCommandHandler_NonCommandNotHandled(t *testing.T) {
	al := newCovLoop(t)
	ch := newCommandHandler(al)
	if got, ok := ch.handleCommand(context.Background(), bus.InboundMessage{Content: "plain hello"}); ok {
		t.Errorf("non-command should not be handled: %q", got)
	}
}
