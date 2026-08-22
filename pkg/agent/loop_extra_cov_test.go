// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// loop.go — HandleGoalCommand
// ============================================================================

func TestHandleGoalCommand_NoHandler(t *testing.T) {
	al := newTestAgentLoop(t)
	al.commandHandler = nil
	if got := al.HandleGoalCommand("s", nil); !strings.Contains(got, "not initialized") {
		t.Errorf("got %q", got)
	}
}

func TestHandleGoalCommand_Usage(t *testing.T) {
	al := newTestAgentLoop(t)
	if got := al.HandleGoalCommand("native:g", nil); !strings.Contains(got, "Uso: /goal") {
		t.Errorf("usage: %q", got)
	}
}

func TestHandleGoalCommand_SetAndStatus(t *testing.T) {
	al := newTestAgentLoop(t)
	// Set a goal (default args branch))
	got := al.HandleGoalCommand("native:g2", []string{"fix", "the", "build"})
	if !strings.Contains(got, "Objetivo establecido") {
		t.Errorf("set: %q", got)
	}
	// status shows current goal
	got = al.HandleGoalCommand("native:g2", []string{"status"})
	if !strings.Contains(got, "fix the build") {
		t.Errorf("status: %q", got)
	}
	// pause
	got = al.HandleGoalCommand("native:g2", []string{"pause"})
	if !strings.Contains(got, "Objetivo pausado") {
		t.Errorf("pause: %q", got)
	}
	// resume
	got = al.HandleGoalCommand("native:g2", []string{"resume"})
	if !strings.Contains(got, "Objetivo reanudado") {
		t.Errorf("resume: %q", got)
	}
	// clear
	got = al.HandleGoalCommand("native:g2", []string{"clear"})
	if !strings.Contains(got, "Objetivo eliminado") {
		t.Errorf("clear: %q", got)
	}
}

func TestHandleGoalCommand_Defaults(t *testing.T) {
	al := newTestAgentLoop(t)
	// pause with no active goal
	if got := al.HandleGoalCommand("native:np", []string{"pause"}); !strings.Contains(got, "No hay objetivo") {
		t.Errorf("pause no goal: %q", got)
	}
	if got := al.HandleGoalCommand("native:np", []string{"resume"}); !strings.Contains(got, "No hay objetivo pausado") {
		t.Errorf("resume no goal: %q", got)
	}
	if got := al.HandleGoalCommand("native:np", []string{"clear"}); !strings.Contains(got, "No hay objetivo") {
		t.Errorf("clear no goal: %q", got)
	}
	if got := al.HandleGoalCommand("native:np", []string{"status"}); !strings.Contains(got, "No hay objetivo activo") {
		t.Errorf("status no goal: %q", got)
	}
	// empty goal text with --turns
	if got := al.HandleGoalCommand("native:np", []string{"--turns", "3"}); !strings.Contains(got, "Se requiere un texto") {
		t.Errorf("empty text: %q", got)
	}
}

// ============================================================================
// llm_runner.go — transientLLMBackoff & emptyRetryBackoff
// ============================================================================

func TestTransientLLMBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 5 * time.Second},
		{1, 15 * time.Second},
		{2, 30 * time.Second},
		{5, 30 * time.Second},
	}
	for _, c := range cases {
		if got := transientLLMBackoff(c.attempt); got != c.want {
			t.Errorf("transientLLMBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

func TestEmptyRetryBackoff(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 3 * time.Second},
		{9, 3 * time.Second},
	}
	for _, c := range cases {
		if got := emptyRetryBackoff(c.attempt); got != c.want {
			t.Errorf("emptyRetryBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

// ============================================================================
// llm_caller.go — streamingState onChunk/onReasoning
// ============================================================================

func TestStreamingState_OnChunk(t *testing.T) {
	s := &streamingState{}
	called := false
	wrapped := s.onChunk(func(chunk string, done bool) {
		called = true
	})
	wrapped("hi", false)
	wrapped("", true)
	if !called {
		t.Error("delegate not called")
	}
	if !s.chunked {
		t.Error("expected chunked=true when a non-empty chunk arrives")
	}
}

func TestStreamingState_OnReasoning(t *testing.T) {
	s := &streamingState{}
	wrapped := s.onReasoning(nil)
	if wrapped != nil {
		t.Error("nil delegate should produce nil wrapper")
	}
	wrapped2 := s.onReasoning(func(chunk string) {})
	wrapped2("deep")
	if !s.chunked {
		t.Error("expected chunked=true after reasoning chunk")
	}
}

// ============================================================================
// goal.go — loadFromDisk / loadFromLegacyFiles / loadFromRepo
// ============================================================================

func TestGoalLoadFromLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	// Write a valid goal file + an invalid one + skip dirs/non-json
	valid := &Goal{SessionKey: "s1", Text: "goal one", Status: GoalActive}
	writeGoalFile(t, dir, "s1.json", valid)
	os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("not json"), 0644)
	os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("x"), 0644)

	gm := NewGoalManager(dir)
	// Don't call SetStore; use legacy load path by just loading from disk.
	// loadFromDisk chooses loadFromLegacyFiles when repo is nil.
	gm.loadFromDisk()
	for k := range gm.goals {
		if k == "s1" {
			return
		}
	}
	// goals map has the legacy goal keyed by s1 (SessionKey).
	if gm.Get("s1") == nil {
		t.Error("expected goal s1 loaded from legacy files")
	}
}

func TestGoalLoadFromLegacyFiles_NoDir(t *testing.T) {
	gm := NewGoalManager("")
	gm.loadFromDisk() // should not panic with empty dir
}

func TestGoalLoadFromRepo_EmptyMigratesLegacy(t *testing.T) {
	s := openGoalStore(t)
	repo := s.Goals()
	dir := t.TempDir()
	valid := &Goal{SessionKey: "legacy", Text: "legacy goal", Status: GoalActive}
	writeGoalFile(t, dir, "legacy.json", valid)

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	// loadFromDisk with repo -> loadFromRepo. Repo empty -> migrate legacy.
	gm.loadFromDisk()
	if gm.Get("legacy") == nil {
		t.Error("expected legacy goal migrated from repo-empty path")
	}
	s.Close()
}

func writeGoalFile(t *testing.T, dir, name string, g *Goal) {
	t.Helper()
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal goal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatalf("write goal file: %v", err)
	}
}

// ============================================================================
// tool_coordinator.go — markSubagentDelivered & continueSubagentTask
// ============================================================================

func TestToolCoordinator_ContinueSubagentTask_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	provider := &mockProvider{mockResponse: "resp"}
	svc := tools.NewSubagentManager(provider, "m", tmpDir, al.bus, 5)
	managers := map[string]*tools.SubagentManager{al.getDefaultAgentID(): svc}
	tc := newToolCoordinatorWithSubagents(al, managers, map[string]*tools.BackgroundProcessManager{})
	_, err := tc.continueSubagentTask(context.Background(), "s", "missing-task", "guidance")
	if err == nil {
		t.Error("expected error for missing task")
	}
}
