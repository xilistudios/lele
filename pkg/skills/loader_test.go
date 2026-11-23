package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSkillsInfoValidate(t *testing.T) {
	testcases := []struct {
		name        string
		skillName   string
		description string
		wantErr     bool
		errContains []string
	}{
		{
			name:        "valid-skill",
			skillName:   "valid-skill",
			description: "a valid skill description",
			wantErr:     false,
		},
		{
			name:        "empty-name",
			skillName:   "",
			description: "description without name",
			wantErr:     true,
			errContains: []string{"name is required"},
		},
		{
			name:        "empty-description",
			skillName:   "skill-without-description",
			description: "",
			wantErr:     true,
			errContains: []string{"description is required"},
		},
		{
			name:        "empty-both",
			skillName:   "",
			description: "",
			wantErr:     true,
			errContains: []string{"name is required", "description is required"},
		},
		{
			name:        "name-with-spaces",
			skillName:   "skill with spaces",
			description: "invalid name with spaces",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
		{
			name:        "name-with-underscore",
			skillName:   "skill_underscore",
			description: "invalid name with underscore",
			wantErr:     true,
			errContains: []string{"name must be alphanumeric with hyphens"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			info := SkillInfo{
				Name:        tc.skillName,
				Description: tc.description,
			}
			err := info.validate()
			if tc.wantErr {
				assert.Error(t, err)
				for _, msg := range tc.errContains {
					assert.ErrorContains(t, err, msg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestExtractFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name           string
		content        string
		expectedName   string
		expectedDesc   string
		lineEndingType string
	}{
		{
			name:           "unix-line-endings",
			lineEndingType: "Unix (\\n)",
			content:        "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "windows-line-endings",
			lineEndingType: "Windows (\\r\\n)",
			content:        "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
		{
			name:           "classic-mac-line-endings",
			lineEndingType: "Classic Mac (\\r)",
			content:        "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedName:   "test-skill",
			expectedDesc:   "A test skill",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			frontmatter := sl.extractFrontmatter(tc.content)
			assert.NotEmpty(t, frontmatter, "Frontmatter should be extracted for %s line endings", tc.lineEndingType)

			yamlMeta := sl.parseSimpleYAML(frontmatter)
			assert.Equal(t, tc.expectedName, yamlMeta["name"], "Name should be correctly parsed from frontmatter with %s line endings", tc.lineEndingType)
			assert.Equal(t, tc.expectedDesc, yamlMeta["description"], "Description should be correctly parsed from frontmatter with %s line endings", tc.lineEndingType)
		})
	}
}

func TestStripFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}

	testcases := []struct {
		name            string
		content         string
		expectedContent string
		lineEndingType  string
	}{
		{
			name:            "unix-line-endings",
			lineEndingType:  "Unix (\\n)",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings",
			lineEndingType:  "Windows (\\r\\n)",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "classic-mac-line-endings",
			lineEndingType:  "Classic Mac (\\r)",
			content:         "---\rname: test-skill\rdescription: A test skill\r---\r\r# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "unix-line-endings-without-trailing-newline",
			lineEndingType:  "Unix (\\n) without trailing newline",
			content:         "---\nname: test-skill\ndescription: A test skill\n---\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "windows-line-endings-without-trailing-newline",
			lineEndingType:  "Windows (\\r\\n) without trailing newline",
			content:         "---\r\nname: test-skill\r\ndescription: A test skill\r\n---\r\n# Skill Content",
			expectedContent: "# Skill Content",
		},
		{
			name:            "no-frontmatter",
			lineEndingType:  "No frontmatter",
			content:         "# Skill Content\n\nSome content here.",
			expectedContent: "# Skill Content\n\nSome content here.",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			result := sl.stripFrontmatter(tc.content)
			assert.Equal(t, tc.expectedContent, result, "Frontmatter should be stripped correctly for %s", tc.lineEndingType)
		})
	}
}

func TestSkillsLoader_LoadSkillTraversal(t *testing.T) {
	workspace := t.TempDir()

	secret := filepath.Join(workspace, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0644); err != nil {
		t.Fatal(err)
	}

	sl := newSkillLoader(t, workspace, skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"})

	testcases := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "dot-dot", input: ".."},
		{name: "parent-dir", input: "../secret"},
		{name: "slash", input: "a/b"},
		{name: "backslash", input: `a\b`},
		{name: "encoded-slash", input: "..%2f"},
		{name: "nested", input: "sub/dir"},
		{name: "escape", input: "foo/../bar"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			content, ok := sl.LoadSkill(tc.input)
			if ok {
				t.Errorf("LoadSkill(%q) should return false, got content %q", tc.input, content)
			}
			if strings.Contains(content, "TOPSECRET") {
				t.Errorf("LoadSkill(%q) leaked secret file content", tc.input)
			}
		})
	}
}

func TestSkillsLoader_LoadSkillValidName(t *testing.T) {
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, "skills", "good-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("---\nname: good-skill\ndescription: test\n---\nhello body"), 0644); err != nil {
		t.Fatal(err)
	}

	sl := NewSkillsLoader(workspace, "", "")
	content, ok := sl.LoadSkill("good-skill")
	if !ok {
		t.Fatal("expected valid skill to load")
	}
	if !strings.Contains(content, "hello body") {
		t.Errorf("expected skill body in content, got: %q", content)
	}
}

func newSummaryLoader(t *testing.T, names ...string) *SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	for _, name := range names {
		skillDir := filepath.Join(workspace, "skills", name)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: desc of " + name + "\n---\nbody"
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewSkillsLoader(workspace, "", "")
}

type skillFixture struct {
	Dir  string
	Name string
}

func newSkillLoader(t *testing.T, workspace string, fixtures ...skillFixture) *SkillsLoader {
	t.Helper()
	for _, f := range fixtures {
		skillDir := filepath.Join(workspace, "skills", f.Dir)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + f.Name + "\ndescription: desc of " + f.Name + "\n---\nbody of " + f.Name
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewSkillsLoader(workspace, "", "")
}

func TestSkillsLoader_LoadSkillResolvesFrontmatterName(t *testing.T) {
	sl := newSkillLoader(t, t.TempDir(), skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"})

	listed := sl.ListSkills()
	if len(listed) != 1 || listed[0].Name != "pdf-toolkit" {
		t.Fatalf("ListSkills() = %v, want the single frontmatter name pdf-toolkit", listed)
	}

	content, ok := sl.LoadSkill("pdf-toolkit")
	if !ok {
		t.Fatal("LoadSkill must resolve the name that ListSkills advertises")
	}
	if !strings.Contains(content, "body of pdf-toolkit") {
		t.Fatalf("LoadSkill returned %q, want the skill body", content)
	}
	if strings.Contains(content, "description:") {
		t.Errorf("LoadSkill leaked the frontmatter: %q", content)
	}
}

func TestSkillsLoader_LoadSkillMatchesNamesCaseInsensitively(t *testing.T) {
	sl := newSkillLoader(t, t.TempDir(),
		skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"},
		skillFixture{Dir: "plain-skill", Name: "plain-skill"},
	)

	testcases := []struct {
		name string
		in   string
		body string
	}{
		{name: "frontmatter-name-upper", in: "PDF-Toolkit", body: "body of pdf-toolkit"},
		{name: "frontmatter-name-all-caps", in: "PDF-TOOLKIT", body: "body of pdf-toolkit"},
		{name: "directory-name", in: "PDF-Tools", body: "body of pdf-toolkit"},
		{name: "lowercase-directory", in: "PLAIN-SKILL", body: "body of plain-skill"},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			content, ok := sl.LoadSkill(tc.in)
			if !ok {
				t.Fatalf("LoadSkill(%q) failed, want the skill body", tc.in)
			}
			if !strings.Contains(content, tc.body) {
				t.Errorf("LoadSkill(%q) = %q, want %q", tc.in, content, tc.body)
			}
		})
	}
}

func TestSkillsLoader_LoadSkillUnknownName(t *testing.T) {
	sl := newSkillLoader(t, t.TempDir(), skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"})
	if content, ok := sl.LoadSkill("ghost"); ok {
		t.Fatalf("LoadSkill(ghost) = %q, want false", content)
	}
}

func TestSkillsLoader_LoadSkillsForContextResolvesFrontmatterName(t *testing.T) {
	sl := newSkillLoader(t, t.TempDir(), skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"})

	content := sl.LoadSkillsForContext([]string{"pdf-toolkit"})
	if content == "" {
		t.Fatal("LoadSkillsForContext dropped a skill advertised by its frontmatter name")
	}
	if !strings.Contains(content, "### Skill: pdf-toolkit") || !strings.Contains(content, "body of pdf-toolkit") {
		t.Fatalf("LoadSkillsForContext = %q, want the skill body under its name", content)
	}
}

func summaryNames(t *testing.T, summary string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "<name>") && strings.HasSuffix(line, "</name>") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(line, "<name>"), "</name>"))
		}
	}
	return out
}

func TestBuildSkillsSummary_FilterSemantics(t *testing.T) {
	testcases := []struct {
		name     string
		filter   []string
		expected []string
	}{
		{name: "nil-filter-lists-all", filter: nil, expected: []string{"alpha", "beta", "gamma"}},
		{name: "empty-filter-lists-all", filter: []string{}, expected: []string{"alpha", "beta", "gamma"}},
		{name: "subset", filter: []string{"beta"}, expected: []string{"beta"}},
		{name: "multiple-and-unordered", filter: []string{"gamma", "alpha"}, expected: []string{"alpha", "gamma"}},
		{name: "unknown-name-is-not-an-error", filter: []string{"ghost"}, expected: nil},
		{name: "subset-with-unknown-mixed-in", filter: []string{"alpha", "ghost"}, expected: []string{"alpha"}},
		{name: "whitespace-in-filter-is-trimmed", filter: []string{"  beta\t"}, expected: []string{"beta"}},
		{name: "case-sensitive", filter: []string{"Alpha"}, expected: nil},
		{name: "blank-entries-only-lists-nothing", filter: []string{"  "}, expected: nil},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			sl := newSummaryLoader(t, "alpha", "beta", "gamma")
			summary := sl.BuildSkillsSummary(tc.filter)

			got := summaryNames(t, summary)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected skills %v, got %v (summary:\n%s)", tc.expected, got, summary)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("skill %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}

			if len(tc.expected) == 0 {
				if summary != "" {
					t.Errorf("expected empty summary, got:\n%s", summary)
				}
				return
			}
			if !strings.HasPrefix(summary, "<skills>\n") || !strings.HasSuffix(summary, "\n</skills>") {
				t.Errorf("summary lost its wrapper:\n%s", summary)
			}
			for _, n := range tc.expected {
				if !strings.Contains(summary, "<description>desc of "+n+"</description>") {
					t.Errorf("expected description for %q in:\n%s", n, summary)
				}
			}
		})
	}
}

func TestBuildSkillsSummary_SkipsDisabledSkillsEvenIfFiltered(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta")

	cfg := &WorkspaceSkillsConfig{Disabled: []string{"alpha"}}
	sl.SetConfigManager(&WorkspaceConfigManager{workspace: "test", config: cfg})

	summary := sl.BuildSkillsSummary([]string{"alpha", "beta"})
	got := summaryNames(t, summary)
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("expected only beta (alpha disabled), got %v:\n%s", got, summary)
	}
}

func TestBuildSkillsSummary_NoSkillsInstalled(t *testing.T) {
	sl := newSummaryLoader(t)
	if s := sl.BuildSkillsSummary(nil); s != "" {
		t.Errorf("expected empty summary with no skills, got:\n%s", s)
	}
	if s := sl.BuildSkillsSummary([]string{"alpha"}); s != "" {
		t.Errorf("expected empty summary with no skills and a filter, got:\n%s", s)
	}
}
