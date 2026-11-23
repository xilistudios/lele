package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDirectorySkill(t *testing.T, root, dir, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(root, dir)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", skillDir, err)
	}
	frontmatter := "---\nname: " + name + "\n"
	if description != "" {
		frontmatter += "description: " + description + "\n"
	}
	frontmatter += "---\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(frontmatter+body), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md): %v", err)
	}
}

func newDirectorySkillLoader(t *testing.T, directory string, workspaceFixtures, globalFixtures []skillFixture) *SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	global := t.TempDir()
	writeSkillFixtures(t, filepath.Join(workspace, "skills"), workspaceFixtures)
	writeSkillFixtures(t, global, globalFixtures)
	return NewSkillsLoader(workspace, global, "", directory)
}

func TestSkillsLoader_DirectorySkillIsListedAndLoaded(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, directory, "myskill", "myskill", "the project skill", "project body")

	sl := newDirectorySkillLoader(t, directory, nil, nil)

	info, ok := catalogEntry(sl.ListSkills(), "myskill")
	if !ok {
		t.Fatalf("ListSkills() = %v, want the directory skill", catalogNames(sl.ListSkills()))
	}
	if info.Source != "directory" {
		t.Fatalf("Source = %q, want directory", info.Source)
	}
	if info.Path != filepath.Join(directory, "myskill", "SKILL.md") {
		t.Fatalf("Path = %q, want the directory SKILL.md", info.Path)
	}

	body, ok := sl.LoadSkill("myskill")
	if !ok {
		t.Fatal("LoadSkill(myskill) must resolve the directory skill")
	}
	if strings.TrimSpace(body) != "project body" {
		t.Fatalf("LoadSkill(myskill) = %q, want the project body", body)
	}
}

func TestSkillsLoader_DirectorySkillWinsNameCollision(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, directory, "foo", "foo", "directory foo", "directory body")

	sl := newDirectorySkillLoader(t, directory,
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "workspace body"}},
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "global body"}},
	)

	listed := sl.ListSkills()
	if len(listed) != 1 {
		t.Fatalf("ListSkills() = %v, want exactly one entry for foo", catalogNames(listed))
	}
	if listed[0].Source != "directory" {
		t.Fatalf("Source = %q, want directory", listed[0].Source)
	}

	body, ok := sl.LoadSkill("foo")
	if !ok {
		t.Fatal("LoadSkill(foo) must resolve")
	}
	if strings.TrimSpace(body) != "directory body" {
		t.Fatalf("LoadSkill(foo) = %q, want the directory body", body)
	}
}

func TestSkillsLoader_AbsentDirectoryRootAddsNothing(t *testing.T) {
	workspaceFixtures := []skillFixture{{Dir: "alpha", Name: "alpha"}}

	for _, tc := range []struct {
		name      string
		directory string
	}{
		{name: "empty", directory: ""},
		{name: "missing", directory: filepath.Join(t.TempDir(), ".lele", "skills")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sl := newDirectorySkillLoader(t, tc.directory, workspaceFixtures, nil)

			listed := sl.ListSkills()
			if len(listed) != 1 || listed[0].Name != "alpha" || listed[0].Source != "workspace" {
				t.Fatalf("ListSkills() = %v, want only the workspace skill alpha", catalogNames(listed))
			}

			if _, ok := sl.LoadSkill("alpha"); !ok {
				t.Fatal("LoadSkill(alpha) must keep working without a directory root")
			}
			if _, ok := sl.LoadSkill("myskill"); ok {
				t.Fatal("LoadSkill(myskill) must not resolve without a directory root")
			}
		})
	}
}

func TestSkillsLoader_InvalidDirectorySkillIsSkippedAndDoesNotClaimItsName(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, directory, "broken", "broken", "", "no description")
	writeDirectorySkill(t, directory, "valid", "valid", "a valid project skill", "valid body")

	sl := newDirectorySkillLoader(t, directory, nil,
		[]skillFixture{{Dir: "broken", Name: "broken", Body: "global body"}},
	)

	listed := sl.ListSkills()
	if len(listed) != 2 {
		t.Fatalf("ListSkills() = %v, want valid from directory and broken from global", catalogNames(listed))
	}
	valid, ok := catalogEntry(listed, "valid")
	if !ok || valid.Source != "directory" || valid.Description != "a valid project skill" {
		t.Fatalf("ListSkills() = %v, want valid from directory", catalogNames(listed))
	}
	broken, ok := catalogEntry(listed, "broken")
	if !ok || broken.Source != "global" {
		t.Fatalf("ListSkills() = %v, want the invalid directory skill to leave broken to global", catalogNames(listed))
	}
	if body, ok := sl.LoadSkill("valid"); !ok || strings.TrimSpace(body) != "valid body" {
		t.Fatalf("LoadSkill(valid) = %q, %v", body, ok)
	}
}
