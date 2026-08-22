package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillsInstallBuiltinCmd_Success creates a fake ./lele/skills layout in a
// temp working directory, then verifies one builtin skill is copied over
// (success branch). This covers copyDirectory + os.MkdirAll success paths.
func TestSkillsInstallBuiltinCmd_Success(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(cwd)

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	// Create ./lele/skills/weather/SKILL.md
	builtinSkill := filepath.Join(root, "lele", "skills", "weather")
	if err := os.MkdirAll(builtinSkill, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(builtinSkill, "SKILL.md"), []byte("---\nname: weather\n---\n"), 0644)

	ws := filepath.Join(root, "ws")
	out := runCmd(func() { skillsInstallBuiltinCmd(ws) })
	if !strings.Contains(out, "All builtin skills installed") {
		t.Errorf("expected completion, got: %s", out)
	}
	// weather skill should have been copied.
	copied := filepath.Join(ws, "skills", "weather", "SKILL.md")
	if _, err := os.Stat(copied); err != nil {
		t.Errorf("expected weather skill copied, err=%v", err)
	}
}

// TestSkillsInstallBuiltinCmd_CopyFail forces copyDirectory failure by making
// workspacePath uncreatable (a file in its place).
func TestSkillsInstallBuiltinCmd_CopyFail(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer os.Chdir(cwd)

	root := t.TempDir()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	builtinSkill := filepath.Join(root, "lele", "skills", "weather")
	if err := os.MkdirAll(builtinSkill, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(builtinSkill, "SKILL.md"), []byte("x"), 0644)

	ws := filepath.Join(root, "ws")
	os.MkdirAll(filepath.Join(ws, "skills"), 0755)
	os.WriteFile(filepath.Join(ws, "skills", "weather"), []byte("blocker"), 0644)

	out := runCmd(func() { skillsInstallBuiltinCmd(ws) })
	if !strings.Contains(out, "Failed to copy") && !strings.Contains(out, "Failed to create directory") {
		t.Errorf("expected a failure message, got: %s", out)
	}
}
