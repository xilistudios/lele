package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProjectSkill(t *testing.T, cwd, name string) {
	t.Helper()
	dir := filepath.Join(cwd, ".lele", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	body := "---\nname: " + name + "\ndescription: the project " + name + " skill\n---\n\nproject body of " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
}

func TestNewContextBuilder_ResolvesProjectSkillsFromWorkingDirectory(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	writeProjectSkill(t, cwd, "project-skill")
	t.Chdir(cwd)

	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)

	loader := cb.SkillsLoader()
	body, ok := loader.LoadSkill("project-skill")
	if !ok {
		t.Fatalf("LoadSkill(project-skill) did not resolve the skill under %s", cwd)
	}
	if !strings.Contains(body, "project body of project-skill") {
		t.Fatalf("LoadSkill(project-skill) = %q, want the project body", body)
	}

	listed := false
	for _, s := range loader.ListSkills() {
		if s.Name == "project-skill" && s.Source == "directory" {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("ListSkills() does not advertise project-skill as a directory skill: %v", loader.ListSkills())
	}

	if !strings.Contains(cb.GetInitialContext(), "project-skill") {
		t.Fatal("the prompt does not carry the project skill, so the model cannot know it exists")
	}
}
