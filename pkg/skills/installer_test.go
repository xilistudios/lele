package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSkillInstaller_UninstallTraversal verifies that path traversal in
// skillName is rejected instead of deleting arbitrary directories.
func TestSkillInstaller_UninstallTraversal(t *testing.T) {
	workspace := t.TempDir()

	// Create a directory outside the workspace that must NOT be deleted.
	victim := filepath.Join(workspace, "..", "victim")
	if err := os.MkdirAll(victim, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(victim)

	si := NewSkillInstaller(workspace)

	err := si.Uninstall("../../victim")
	if err == nil {
		t.Fatal("expected traversal skill name to be rejected")
	}

	if _, err := os.Stat(victim); os.IsNotExist(err) {
		t.Error("victim directory was deleted — traversal not prevented")
	}
}

// TestSkillInstaller_UninstallInvalidNames verifies various malformed names
// are rejected with an "invalid skill name" error.
func TestSkillInstaller_UninstallInvalidNames(t *testing.T) {
	cases := []string{
		"",
		"../../.ssh",
		"..\\..\\x",
		"foo/bar",
		"foo\\bar",
		"..",
		"a..b",
	}

	for _, name := range cases {
		si := NewSkillInstaller(t.TempDir())
		err := si.Uninstall(name)
		if err == nil {
			t.Errorf("expected error for skill name %q, got nil", name)
		}
	}
}

// TestSkillInstaller_UninstallValidSkill verifies a legitimate skill is still
// removed correctly.
func TestSkillInstaller_UninstallValidSkill(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	si := NewSkillInstaller(workspace)
	if err := si.Uninstall("my-skill"); err != nil {
		t.Fatalf("expected valid uninstall to succeed, got: %v", err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Error("expected skill directory to be removed")
	}
}

// TestSkillInstaller_UninstallNotFound verifies a valid but nonexistent skill
// still returns the "not found" error.
func TestSkillInstaller_UninstallNotFound(t *testing.T) {
	si := NewSkillInstaller(t.TempDir())
	err := si.Uninstall("nonexistent")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}
