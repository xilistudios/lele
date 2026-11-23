package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillUnder(t *testing.T, cwd, relative, name, body string) {
	t.Helper()
	dir := filepath.Join(cwd, relative, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: skill " + name + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
}

func TestNewContextBuilder_ResolvesAgentsAndDirectoryProjectSkills(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	writeSkillUnder(t, cwd, filepath.Join(".agents", "skills"), "a-skill", "agents body of a-skill")
	writeSkillUnder(t, cwd, filepath.Join(".lele", "skills"), "l-skill", "directory body of l-skill")
	t.Chdir(cwd)

	cb := NewContextBuilder(t.TempDir())
	loader := cb.SkillsLoader()

	agentsBody, ok := loader.LoadSkill("a-skill")
	if !ok {
		t.Fatalf("LoadSkill(a-skill) did not resolve the skill under %s", cwd)
	}
	if !strings.Contains(agentsBody, "agents body of a-skill") {
		t.Fatalf("LoadSkill(a-skill) = %q, want the .agents body", agentsBody)
	}

	directoryBody, ok := loader.LoadSkill("l-skill")
	if !ok {
		t.Fatalf("LoadSkill(l-skill) did not resolve the skill under %s", cwd)
	}
	if !strings.Contains(directoryBody, "directory body of l-skill") {
		t.Fatalf("LoadSkill(l-skill) = %q, want the .lele body", directoryBody)
	}

	sources := make(map[string]string)
	for _, s := range loader.ListSkills() {
		sources[s.Name] = s.Source
	}
	if sources["a-skill"] != "agents" {
		t.Fatalf("ListSkills() source for a-skill = %q, want agents", sources["a-skill"])
	}
	if sources["l-skill"] != "directory" {
		t.Fatalf("ListSkills() source for l-skill = %q, want directory", sources["l-skill"])
	}

	prompt := cb.GetInitialContext()
	for _, name := range []string{"a-skill", "l-skill"} {
		if !strings.Contains(prompt, name) {
			t.Fatalf("the prompt does not carry %s, so the model cannot know it exists", name)
		}
	}
}

func TestNewContextBuilder_AgentsSkillWinsCollisionWithDirectorySkill(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cwd := t.TempDir()
	writeSkillUnder(t, cwd, filepath.Join(".agents", "skills"), "shared", "agents body of shared")
	writeSkillUnder(t, cwd, filepath.Join(".lele", "skills"), "shared", "directory body of shared")
	t.Chdir(cwd)

	cb := NewContextBuilder(t.TempDir())
	loader := cb.SkillsLoader()

	body, ok := loader.LoadSkill("shared")
	if !ok {
		t.Fatalf("LoadSkill(shared) did not resolve the skill under %s", cwd)
	}
	if !strings.Contains(body, "agents body of shared") {
		t.Fatalf("LoadSkill(shared) = %q, want the .agents body", body)
	}

	listed := 0
	for _, s := range loader.ListSkills() {
		if s.Name != "shared" {
			continue
		}
		listed++
		if s.Source != "agents" {
			t.Fatalf("ListSkills() source for shared = %q, want agents", s.Source)
		}
	}
	if listed != 1 {
		t.Fatalf("ListSkills() advertises shared %d times, want exactly once", listed)
	}
	if !loader.SkillResolvable(nil, "shared") {
		t.Fatal("SkillResolvable(shared) must accept the skill listed in the prompt")
	}
}
