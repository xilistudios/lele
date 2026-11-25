package agent

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetWorkspace_RebuildsPromptAndStore(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()

	cb := NewContextBuilder(workspaceA)
	if !strings.Contains(cb.GetInitialContext(), workspaceA) {
		t.Fatalf("precondition: the prompt must render the initial workspace %q", workspaceA)
	}

	cb.SetWorkspace(workspaceB)

	prompt := cb.GetInitialContext()
	if !strings.Contains(prompt, workspaceB) {
		t.Errorf("prompt does not render the new workspace %q", workspaceB)
	}
	if strings.Contains(prompt, workspaceA) {
		t.Errorf("prompt still renders the old workspace %q", workspaceA)
	}
}

func TestSetWorkspace_MemoryWritesToNewDir(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()

	cb := NewContextBuilder(workspaceA)
	if _, err := os.Stat(filepath.Join(workspaceA, "memory")); err != nil {
		t.Fatalf("precondition: the initial memory dir must exist: %v", err)
	}

	cb.SetWorkspace(workspaceB)
	if err := cb.memory.AppendToday("workspace-b-note"); err != nil {
		t.Fatalf("AppendToday failed: %v", err)
	}

	written := memoryFilesContaining(t, filepath.Join(workspaceB, "memory"), "workspace-b-note")
	if len(written) != 1 {
		t.Fatalf("memory writes under the new workspace = %v, want exactly one file", written)
	}
	if leaked := memoryFilesContaining(t, filepath.Join(workspaceA, "memory"), "workspace-b-note"); len(leaked) != 0 {
		t.Errorf("memory write landed in the old workspace: %v", leaked)
	}
	if got := cb.memory.ReadToday(); !strings.Contains(got, "workspace-b-note") {
		t.Errorf("ReadToday from the rebuilt store = %q, want the note written after the switch", got)
	}
}

func TestSetWorkspace_NoOpWhenUnchanged(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()

	cb := NewContextBuilder(workspace)
	if cb.GetInitialContext() == "" {
		t.Fatal("precondition: the prompt must be cached")
	}
	loader := cb.SkillsLoader()
	store := cb.memory

	cb.SetWorkspace(workspace)

	if cb.SkillsLoader() != loader {
		t.Error("skills loader rebuilt for an unchanged workspace")
	}
	if cb.memory != store {
		t.Error("memory store rebuilt for an unchanged workspace")
	}
	if cb.initialContext == "" {
		t.Error("cached prompt dropped for an unchanged workspace")
	}
}

func TestSetWorkspace_SkillsLoaderSeedsNewDir(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "old-workspace-skill", "the old workspace skill")
	writeTestSkill(t, workspaceB, "new-workspace-skill", "the new workspace skill")

	cb := NewContextBuilder(workspaceA)
	if !strings.Contains(cb.GetInitialContext(), "old-workspace-skill") {
		t.Fatal("precondition: the prompt must list the initial workspace skill")
	}
	if _, ok := cb.SkillsLoader().LoadSkill("old-workspace-skill"); !ok {
		t.Fatal("precondition: the initial loader must resolve the initial workspace skill")
	}

	cb.SetWorkspace(workspaceB)

	if _, ok := cb.SkillsLoader().LoadSkill("new-workspace-skill"); !ok {
		t.Error("the rebuilt loader does not resolve a skill from the new workspace")
	}
	if _, ok := cb.SkillsLoader().LoadSkill("old-workspace-skill"); ok {
		t.Error("the rebuilt loader still resolves a skill from the old workspace")
	}
	prompt := cb.GetInitialContext()
	if !strings.Contains(prompt, "new-workspace-skill") {
		t.Error("prompt does not list the new workspace skill")
	}
	if strings.Contains(prompt, "old-workspace-skill") {
		t.Error("prompt still lists the old workspace skill")
	}
}

func memoryFilesContaining(t *testing.T, dir, needle string) []string {
	t.Helper()
	var hits []string
	walkErr := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), needle) {
			hits = append(hits, path)
		}
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		t.Fatalf("walk %s failed: %v", dir, walkErr)
	}
	return hits
}
