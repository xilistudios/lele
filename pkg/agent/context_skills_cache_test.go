package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAgentSkill(t *testing.T, workspace, name string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: the %s skill\n---\n\nbody\n", name, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
}

func removeAgentSkill(t *testing.T, workspace, name string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(workspace, "skills", name)); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
}

func TestSkillsLoader_IsThePromptSource(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	writeAgentSkill(t, workspace, "alpha")

	cb := NewContextBuilder(workspace)
	loader := cb.SkillsLoader()
	if loader == nil {
		t.Fatal("SkillsLoader() returned nil")
	}
	if loader != cb.skillsLoader {
		t.Fatal("SkillsLoader() must return the builder's own instance, not a copy")
	}
	if loader.WorkspaceDir() != workspace {
		t.Fatalf("loader workspace = %q, want the agent's own %q", loader.WorkspaceDir(), workspace)
	}

	names := map[string]bool{}
	for _, s := range loader.ListSkills() {
		names[s.Name] = true
	}
	if !names["alpha"] {
		t.Fatalf("loader does not see the workspace skill: %v", names)
	}
	if !strings.Contains(cb.GetInitialContext(), "alpha") {
		t.Fatal("cached prompt does not contain the skill the loader reports")
	}
}

func TestInvalidatePromptCache_PicksUpInstalledSkill(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	writeAgentSkill(t, workspace, "first")

	cb := NewContextBuilder(workspace)
	before := cb.GetInitialContext()
	if !strings.Contains(before, "first") {
		t.Fatal("precondition: 'first' must be in the initial prompt")
	}
	if strings.Contains(before, "second") {
		t.Fatal("precondition: 'second' must not be there yet")
	}

	writeAgentSkill(t, workspace, "second")
	if got := cb.GetInitialContext(); strings.Contains(got, "second") {
		t.Fatal("prompt must be cached: 'second' should not appear without invalidation")
	}

	cb.InvalidatePromptCache()
	after := cb.GetInitialContext()
	if !strings.Contains(after, "second") {
		t.Fatal("installed skill still missing after InvalidatePromptCache")
	}
	if !strings.Contains(after, "first") {
		t.Fatal("invalidation must rebuild, not shrink: 'first' disappeared")
	}
}

func TestInvalidatePromptCache_DropsRemovedSkill(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	writeAgentSkill(t, workspace, "doomed")
	writeAgentSkill(t, workspace, "keeper")

	cb := NewContextBuilder(workspace)
	if got := cb.GetInitialContext(); !strings.Contains(got, "doomed") {
		t.Fatal("precondition: 'doomed' must be in the initial prompt")
	}

	removeAgentSkill(t, workspace, "doomed")
	cb.InvalidatePromptCache()

	after := cb.GetInitialContext()
	if strings.Contains(after, "doomed") {
		t.Fatal("removed skill still advertised after invalidation")
	}
	if !strings.Contains(after, "keeper") {
		t.Fatal("surviving skill disappeared")
	}
}

func TestInvalidatePromptCache_ReflectsToggle(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	writeAgentSkill(t, workspace, "switchable")

	cb := NewContextBuilder(workspace)
	loader := cb.SkillsLoader()
	if err := loader.GetConfigManager().SetDisabled("switchable"); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	cb.InvalidatePromptCache()

	after := cb.GetInitialContext()
	for _, s := range loader.ListSkills() {
		if s.Name == "switchable" && s.Enabled {
			t.Fatal("loader still reports the skill enabled after disabling it")
		}
	}
	if strings.Contains(after, "<name>switchable</name>") {
		t.Fatal("disabled skill still in the prompt's skills block")
	}
}

func TestInvalidatePromptCache_IsIdempotentWhenUncached(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	cb.InvalidatePromptCache()
	cb.InvalidatePromptCache()

	if got := cb.GetInitialContext(); got == "" {
		t.Fatal("builder produced an empty prompt after double invalidation")
	}
}
