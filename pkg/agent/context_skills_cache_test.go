// Tests for the per-agent skills accessors the WebUI endpoints build on:
// ContextBuilder.SkillsLoader() and ContextBuilder.InvalidatePromptCache().
//
// They guard the two halves of the promise the API makes:
//   - the loader handed out IS the one the prompt is rendered from, so a
//     toggle through it cannot describe a state the agent doesn't see;
//   - after installing/removing a skill on disk, InvalidatePromptCache makes
//     the NEXT prompt reflect it. Without this, a skill installed from the UI
//     would stay invisible to the agent until restart.

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeAgentSkill drops a minimal SKILL.md into the builder's workspace,
// exactly where a workspace-scoped install would write.
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

// TestSkillsLoader_IsThePromptSource: the accessor must return the very
// instance that renders the <skills> block — a copy or a differently-scoped
// loader would make the WebUI's list/toggle diverge from the agent.
func TestSkillsLoader_IsThePromptSource(t *testing.T) {
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

	// What the loader lists is what the prompt advertises.
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

// TestInvalidatePromptCache_PicksUpInstalledSkill covers the install path:
// the prompt is cached, so a skill added after the first build is invisible
// until the cache is dropped.
func TestInvalidatePromptCache_PicksUpInstalledSkill(t *testing.T) {
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

	// Simulate what POST .../skills/install does on disk.
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

// TestInvalidatePromptCache_DropsRemovedSkill covers the remove path, the
// other direction of the same bug (advertising a skill that no longer exists
// makes the agent try to read a missing SKILL.md).
func TestInvalidatePromptCache_DropsRemovedSkill(t *testing.T) {
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

// TestInvalidatePromptCache_ReflectsToggle covers enable/disable: the state
// lives in the workspace config, and the loader consults it on every build.
func TestInvalidatePromptCache_ReflectsToggle(t *testing.T) {
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
	// A disabled skill must not be advertised as available.
	if strings.Contains(after, "<name>switchable</name>") {
		t.Fatal("disabled skill still in the prompt's skills block")
	}
}

// TestInvalidatePromptCache_IsIdempotentAndSafeWhenUncached: calling it before
// anything was cached must not panic, and must leave the builder functional.
func TestInvalidatePromptCache_IsIdempotentWhenUncached(t *testing.T) {
	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	cb.InvalidatePromptCache()
	cb.InvalidatePromptCache()

	if got := cb.GetInitialContext(); got == "" {
		t.Fatal("builder produced an empty prompt after double invalidation")
	}
}
