package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func makeSkillsLoader(t *testing.T) (*skills.SkillsLoader, string, string) {
	t.Helper()
	ws := t.TempDir()
	global := t.TempDir()
	builtin := t.TempDir()
	// Create a workspace skill.
	skillDir := filepath.Join(ws, "skills", "weather")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	content := "---\nname: weather\ndescription: Get weather\n---\n# weather\n"
	if err := os.WriteFile(skillFile, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return skills.NewSkillsLoader(ws, global, builtin), ws, builtin
}

func TestSkillsListCmd_WithSkills(t *testing.T) {
	loader, _, _ := makeSkillsLoader(t)
	out := runCmd(func() { skillsListCmd(loader) })
	if !strings.Contains(out, "weather") {
		t.Errorf("list should include weather skill, got: %s", out)
	}
	if !strings.Contains(out, "Installed Skills") {
		t.Errorf("list should include header, got: %s", out)
	}
}

func TestSkillsListCmd_Empty(t *testing.T) {
	loader := skills.NewSkillsLoader(t.TempDir(), t.TempDir(), t.TempDir())
	out := runCmd(func() { skillsListCmd(loader) })
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("expected no skills message, got: %s", out)
	}
}

func TestSkillsShowCmd_Found(t *testing.T) {
	loader, _, _ := makeSkillsLoader(t)
	out := runCmd(func() { skillsShowCmd(loader, "weather") })
	if !strings.Contains(out, "weather") {
		t.Errorf("show should include skill name, got: %s", out)
	}
}

func TestSkillsShowCmd_NotFound(t *testing.T) {
	loader, _, _ := makeSkillsLoader(t)
	out := runCmd(func() { skillsShowCmd(loader, "nonexistent") })
	if !strings.Contains(out, "not found") {
		t.Errorf("show should say not found, got: %s", out)
	}
}

func TestSkillsRemoveCmd_Success(t *testing.T) {
	ws := t.TempDir()
	installer := skills.NewSkillInstaller(ws)
	skillDir := filepath.Join(ws, "skills", "testskill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	out := runCmd(func() { skillsRemoveCmd(installer, "testskill") })
	if !strings.Contains(out, "removed successfully") {
		t.Errorf("remove should succeed, got: %s", out)
	}
}

func TestSkillsInstallBuiltinCmd_MissingDir(t *testing.T) {
	// builtin dir ./lele/skills doesn't exist in test root, so all skills skipped.
	out := runCmd(func() { skillsInstallBuiltinCmd(t.TempDir()) })
	if !strings.Contains(out, "All builtin skills installed") {
		t.Errorf("expected completion message, got: %s", out)
	}
}

func TestSkillsListBuiltinCmd_NoDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// No builtin skills dir exists -> reads fail gracefully.
	out := runCmd(func() { skillsListBuiltinCmd() })
	// Accept either error message or empty listing.
	if out == "" {
		t.Error("expected some output", out)
	}
}

func TestSkillsListBuiltinCmd_WithSkills(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	// Workspace: dir/workspace. Builtin dir resolves to dir/lele/skills.
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "workspace")
	saveConfigAt(t, dir, cfg)

	builtinDir := filepath.Join(filepath.Dir(cfg.Agents.Defaults.Workspace), "lele", "skills")
	skillDir := filepath.Join(builtinDir, "weather")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := "---\nname: weather\ndescription: Get weather\n---\n# weather\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	// A non-skill file should be ignored.
	os.WriteFile(filepath.Join(builtinDir, "README.md"), []byte("readme"), 0644)

	out := runCmd(skillsListBuiltinCmd)
	if !strings.Contains(out, "weather") {
		t.Errorf("should list weather skill, got: %s", out)
	}
	if !strings.Contains(out, "Available Builtin Skills") {
		t.Errorf("expected header, got: %s", out)
	}
}

func TestSkillsListBuiltinCmd_EmptyDir(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "workspace")
	saveConfigAt(t, dir, cfg)
	builtinDir := filepath.Join(filepath.Dir(cfg.Agents.Defaults.Workspace), "lele", "skills")
	os.MkdirAll(builtinDir, 0755)

	out := runCmd(skillsListBuiltinCmd)
	if !strings.Contains(out, "No builtin skills available") {
		t.Errorf("expected no skills message, got: %s", out)
	}
}

func TestSkillsInstallCmd_NoArgs(t *testing.T) {
	replaceArgs(t, []string{"lele", "skills", "install"})
	installer := skills.NewSkillInstaller(t.TempDir())
	out := runCmd(func() { skillsInstallCmd(installer) })
	if !strings.Contains(out, "Usage: lele skills install") {
		t.Errorf("expected usage message, got: %s", out)
	}
}

// SkillInstaller helper to build a loader using a workspace with an installed
// skill but listing source.
func TestSkillsSearchCmd_ErrorFallback(t *testing.T) {
	installer := skills.NewSkillInstaller(t.TempDir())
	// ListAvailableSkills requires network; it will error -> prints error and returns.
	out := runCmd(func() { skillsSearchCmd(installer) })
	if !strings.Contains(out, "Searching for available skills") {
		t.Errorf("expected searching message, got: %s", out)
	}
}