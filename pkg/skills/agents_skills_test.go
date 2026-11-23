package skills

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newAgentsSkillLoader(t *testing.T, agents, directory string, workspaceFixtures, globalFixtures []skillFixture) *SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	global := t.TempDir()
	writeSkillFixtures(t, filepath.Join(workspace, "skills"), workspaceFixtures)
	writeSkillFixtures(t, global, globalFixtures)
	return NewSkillsLoader(workspace, global, "", directory, agents)
}

func TestSkillsLoader_AgentsSkillIsListedAndLoaded(t *testing.T) {
	agents := filepath.Join(t.TempDir(), ".agents", "skills")
	writeDirectorySkill(t, agents, "myskill", "myskill", "the agents skill", "agents body")

	sl := newAgentsSkillLoader(t, agents, "", nil, nil)

	info, ok := catalogEntry(sl.ListSkills(), "myskill")
	if !ok {
		t.Fatalf("ListSkills() = %v, want the agents skill", catalogNames(sl.ListSkills()))
	}
	if info.Source != "agents" {
		t.Fatalf("Source = %q, want agents", info.Source)
	}
	if info.Path != filepath.Join(agents, "myskill", "SKILL.md") {
		t.Fatalf("Path = %q, want the agents SKILL.md", info.Path)
	}

	body, ok := sl.LoadSkill("myskill")
	if !ok {
		t.Fatal("LoadSkill(myskill) must resolve the agents skill")
	}
	if strings.TrimSpace(body) != "agents body" {
		t.Fatalf("LoadSkill(myskill) = %q, want the agents body", body)
	}
	if !sl.SkillResolvable(nil, "myskill") {
		t.Fatal("SkillResolvable(myskill) must accept the agents skill listed in the prompt")
	}
}

func TestSkillsLoader_AgentsSkillWinsCollisionWithDirectory(t *testing.T) {
	agents := filepath.Join(t.TempDir(), ".agents", "skills")
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, agents, "foo", "foo", "agents foo", "agents body")
	writeDirectorySkill(t, directory, "foo", "foo", "directory foo", "directory body")

	sl := newAgentsSkillLoader(t, agents, directory,
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "workspace body"}},
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "global body"}},
	)

	listed := sl.ListSkills()
	if len(listed) != 1 {
		t.Fatalf("ListSkills() = %v, want exactly one entry for foo", catalogNames(listed))
	}
	if listed[0].Source != "agents" {
		t.Fatalf("Source = %q, want agents", listed[0].Source)
	}
	if listed[0].Path != filepath.Join(agents, "foo", "SKILL.md") {
		t.Fatalf("Path = %q, want the agents SKILL.md", listed[0].Path)
	}

	body, ok := sl.LoadSkill("foo")
	if !ok {
		t.Fatal("LoadSkill(foo) must resolve")
	}
	if strings.TrimSpace(body) != "agents body" {
		t.Fatalf("LoadSkill(foo) = %q, want the agents body", body)
	}
}

func TestSkillsLoader_DirectoryStillWinsCollisionWithProjectRoots(t *testing.T) {
	agents := filepath.Join(t.TempDir(), ".agents", "skills")
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, agents, "agents-only", "agents-only", "the agents only skill", "agents only body")
	writeDirectorySkill(t, directory, "foo", "foo", "directory foo", "directory body")

	sl := newAgentsSkillLoader(t, agents, directory,
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "workspace body"}},
		[]skillFixture{{Dir: "foo", Name: "foo", Body: "global body"}},
	)

	listed := sl.ListSkills()
	assert.ElementsMatch(t, []string{"agents-only", "foo"}, catalogNames(listed))

	foo, ok := catalogEntry(listed, "foo")
	if !ok || foo.Source != "directory" {
		t.Fatalf("ListSkills() = %v, want foo from directory", catalogNames(listed))
	}
	agentsOnly, ok := catalogEntry(listed, "agents-only")
	if !ok || agentsOnly.Source != "agents" {
		t.Fatalf("ListSkills() = %v, want agents-only from agents", catalogNames(listed))
	}

	body, ok := sl.LoadSkill("foo")
	if !ok {
		t.Fatal("LoadSkill(foo) must resolve")
	}
	if strings.TrimSpace(body) != "directory body" {
		t.Fatalf("LoadSkill(foo) = %q, want the directory body", body)
	}
}

func TestSkillsLoader_AbsentAgentsRootAddsNothing(t *testing.T) {
	directory := filepath.Join(t.TempDir(), ".lele", "skills")
	writeDirectorySkill(t, directory, "delta", "delta", "the project delta", "delta body")

	for _, tc := range []struct {
		name   string
		agents string
	}{
		{name: "empty", agents: ""},
		{name: "missing", agents: filepath.Join(t.TempDir(), ".agents", "skills")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sl := newAgentsSkillLoader(t, tc.agents, directory,
				[]skillFixture{{Dir: "alpha", Name: "alpha"}}, nil)

			listed := sl.ListSkills()
			assert.ElementsMatch(t, []string{"alpha", "delta"}, catalogNames(listed))

			alpha, ok := catalogEntry(listed, "alpha")
			if !ok || alpha.Source != "workspace" {
				t.Fatalf("ListSkills() = %v, want alpha from workspace", catalogNames(listed))
			}
			delta, ok := catalogEntry(listed, "delta")
			if !ok || delta.Source != "directory" {
				t.Fatalf("ListSkills() = %v, want delta from directory", catalogNames(listed))
			}

			if _, ok := sl.LoadSkill("alpha"); !ok {
				t.Fatal("LoadSkill(alpha) must keep working without an agents root")
			}
			if body, ok := sl.LoadSkill("delta"); !ok || strings.TrimSpace(body) != "delta body" {
				t.Fatalf("LoadSkill(delta) = (%q, %t), want the directory body", body, ok)
			}
			if _, ok := sl.LoadSkill("myskill"); ok {
				t.Fatal("LoadSkill(myskill) must not resolve without an agents root")
			}
		})
	}
}
