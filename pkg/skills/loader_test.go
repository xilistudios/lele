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
	rootSkill := filepath.Join(workspace, "skills", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(rootSkill), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootSkill, []byte("ROOTSKILL no frontmatter"), 0644); err != nil {
		t.Fatal(err)
	}

	sl := newSkillLoader(t, workspace, skillFixture{Dir: "pdf-tools", Name: "pdf-toolkit"})

	testcases := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "dot", input: "."},
		{name: "dot-dot", input: ".."},
		{name: "hidden", input: ".hidden"},
		{name: "double-dot-substring", input: "x..y"},
		{name: "space", input: "a b"},
		{name: "parent-dir", input: "../secret"},
		{name: "slash", input: "a/b"},
		{name: "backslash", input: `a\b`},
		{name: "encoded-slash", input: "..%2f"},
		{name: "nested", input: "sub/dir"},
		{name: "escape", input: "foo/../bar"},
		{name: "too-long", input: strings.Repeat("a", MaxNameLength+1)},
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
			if strings.Contains(content, "ROOTSKILL") {
				t.Errorf("LoadSkill(%q) read the skills root SKILL.md", tc.input)
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

func TestSkillsLoader_LoadSkillResolvesFolderSafeNames(t *testing.T) {
	sl := newSkillLoader(t, t.TempDir(),
		skillFixture{Dir: "my_skill", Name: "my-skill"},
		skillFixture{Dir: "pdf.tools", Name: "pdf-toolkit"},
		skillFixture{Dir: "Weather", Name: "weather"},
		skillFixture{Dir: "__proto__", Name: "proto-skill"},
		skillFixture{Dir: "-leading", Name: "leading-skill"},
	)

	accepted := []struct {
		name string
		in   string
		body string
	}{
		{name: "underscore-folder", in: "my_skill", body: "body of my-skill"},
		{name: "dotted-folder", in: "pdf.tools", body: "body of pdf-toolkit"},
		{name: "uppercase-folder", in: "Weather", body: "body of weather"},
		{name: "uppercase-folder-lowercase-request", in: "weather", body: "body of weather"},
		{name: "dunder-folder", in: "__proto__", body: "body of proto-skill"},
		{name: "hyphen-prefixed-folder", in: "-leading", body: "body of leading-skill"},
		{name: "frontmatter-name", in: "my-skill", body: "body of my-skill"},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			content, ok := sl.LoadSkill(tc.in)
			if !ok {
				t.Fatalf("LoadSkill(%q) must resolve the folder alias", tc.in)
			}
			if !strings.Contains(content, tc.body) {
				t.Fatalf("LoadSkill(%q) = %q, want %q", tc.in, content, tc.body)
			}
		})
	}

	refused := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "dot", in: "."},
		{name: "dot-dot", in: ".."},
		{name: "hidden", in: ".hidden"},
		{name: "space", in: "a b"},
		{name: "slash", in: "a/b"},
		{name: "backslash", in: `a\b`},
		{name: "parent-dir", in: "../x"},
		{name: "double-dot-substring", in: "x..y"},
		{name: "too-long", in: strings.Repeat("a", MaxNameLength+1)},
	}

	for _, tc := range refused {
		t.Run("refused-"+tc.name, func(t *testing.T) {
			if content, ok := sl.LoadSkill(tc.in); ok {
				t.Fatalf("LoadSkill(%q) = %q, want a refusal", tc.in, content)
			}
		})
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
	Body string
}

func writeSkillFixtures(t *testing.T, root string, fixtures []skillFixture) {
	t.Helper()
	for _, f := range fixtures {
		skillDir := filepath.Join(root, f.Dir)
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			t.Fatal(err)
		}
		body := f.Body
		if body == "" {
			body = "body of " + f.Name
		}
		content := "---\nname: " + f.Name + "\ndescription: desc of " + f.Name + "\n---\n" + body
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func newSkillLoader(t *testing.T, workspace string, fixtures ...skillFixture) *SkillsLoader {
	t.Helper()
	writeSkillFixtures(t, filepath.Join(workspace, "skills"), fixtures)
	return NewSkillsLoader(workspace, "", "")
}

func newTwoRootSkillLoader(t *testing.T, workspaceFixtures, globalFixtures []skillFixture) *SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	global := t.TempDir()
	writeSkillFixtures(t, filepath.Join(workspace, "skills"), workspaceFixtures)
	writeSkillFixtures(t, global, globalFixtures)
	return NewSkillsLoader(workspace, global, "")
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

func catalogNames(list []SkillInfo) []string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name)
	}
	return names
}

func catalogEntry(list []SkillInfo, name string) (SkillInfo, bool) {
	for _, s := range list {
		if s.Name == name {
			return s, true
		}
	}
	return SkillInfo{}, false
}

func TestSkillsLoader_CatalogListsBothSkillsWhenOnlyFoldersCollide(t *testing.T) {
	sl := newTwoRootSkillLoader(t,
		[]skillFixture{{Dir: "bar", Name: "baz", Body: "workspace baz body"}},
		[]skillFixture{{Dir: "bar", Name: "bar", Body: "global bar body"}},
	)

	listed := sl.ListSkills()
	names := catalogNames(listed)
	if len(names) != 2 {
		t.Fatalf("ListSkills() = %v, want both baz and bar", names)
	}
	assert.ElementsMatch(t, []string{"bar", "baz"}, names)

	if content, ok := sl.LoadSkill("bar"); !ok || !strings.Contains(content, "global bar body") {
		t.Fatalf("LoadSkill(bar) = (%q, %t), want the global bar body", content, ok)
	}
	if content, ok := sl.LoadSkill("BAR"); !ok || !strings.Contains(content, "global bar body") {
		t.Fatalf("LoadSkill(BAR) = (%q, %t), want the global bar body", content, ok)
	}
	if content, ok := sl.LoadSkill("baz"); !ok || !strings.Contains(content, "workspace baz body") {
		t.Fatalf("LoadSkill(baz) = (%q, %t), want the workspace baz body", content, ok)
	}
}

func TestSkillsLoader_WorkspaceSkillShadowsGlobalSkillName(t *testing.T) {
	sl := newTwoRootSkillLoader(t,
		[]skillFixture{{Dir: "dup", Name: "dup", Body: "workspace dup body"}},
		[]skillFixture{
			{Dir: "dup", Name: "dup", Body: "global dup body"},
			{Dir: "only-global", Name: "only-global"},
		},
	)

	listed := sl.ListSkills()
	names := catalogNames(listed)
	if len(names) != 2 {
		t.Fatalf("ListSkills() = %v, want dup listed once plus only-global", names)
	}
	assert.ElementsMatch(t, []string{"dup", "only-global"}, names)

	entry, ok := catalogEntry(listed, "dup")
	if !ok || entry.Source != "workspace" {
		t.Fatalf("dup entry = %+v, want the workspace skill to shadow the global one", entry)
	}
	if content, ok := sl.LoadSkill("dup"); !ok || !strings.Contains(content, "workspace dup body") {
		t.Fatalf("LoadSkill(dup) = (%q, %t), want the workspace body", content, ok)
	}
	if content, ok := sl.LoadSkill("only-global"); !ok || !strings.Contains(content, "body of only-global") {
		t.Fatalf("LoadSkill(only-global) = (%q, %t), want the global-only skill", content, ok)
	}
}

func TestSkillsLoader_LoadSkillResolvesTheCatalogOwnerAcrossRoots(t *testing.T) {
	sl := newTwoRootSkillLoader(t,
		[]skillFixture{{Dir: "bar", Name: "baz", Body: "workspace baz body"}},
		[]skillFixture{{Dir: "baz", Name: "baz", Body: "global baz body"}},
	)

	listed := sl.ListSkills()
	if names := catalogNames(listed); len(names) != 1 || names[0] != "baz" {
		t.Fatalf("ListSkills() = %v, want only the workspace baz", names)
	}
	entry, ok := catalogEntry(listed, "baz")
	if !ok || entry.Source != "workspace" {
		t.Fatalf("baz entry = %+v, want the workspace skill to own the name", entry)
	}

	if content, ok := sl.LoadSkill("baz"); !ok || !strings.Contains(content, "workspace baz body") {
		t.Fatalf("LoadSkill(baz) = (%q, %t), want the body of the catalog owner", content, ok)
	}
}

func TestSkillsLoader_CatalogClaimsNamesCaseInsensitively(t *testing.T) {
	sl := newTwoRootSkillLoader(t,
		[]skillFixture{{Dir: "Dup", Name: "Dup", Body: "workspace dup body"}},
		[]skillFixture{{Dir: "dup", Name: "dup", Body: "global dup body"}},
	)

	listed := sl.ListSkills()
	if names := catalogNames(listed); len(names) != 1 || names[0] != "Dup" {
		t.Fatalf("ListSkills() = %v, want a single entry for names that differ only by case", names)
	}
	entry, ok := catalogEntry(listed, "Dup")
	if !ok || entry.Source != "workspace" {
		t.Fatalf("Dup entry = %+v, want the workspace skill to win the case-insensitive claim", entry)
	}

	if content, ok := sl.LoadSkill("dup"); !ok || !strings.Contains(content, "workspace dup body") {
		t.Fatalf("LoadSkill(dup) = (%q, %t), want the workspace body", content, ok)
	}
}

func TestSkillsLoader_SkillResolvable(t *testing.T) {
	workspace := t.TempDir()
	sl := NewSkillsLoader(workspace, "", "")
	writeSkillFixtures(t, filepath.Join(workspace, "skills"), []skillFixture{
		{Dir: "alpha", Name: "alpha"},
		{Dir: "aliased-dir", Name: "aliased"},
		{Dir: "doomed", Name: "doomed"},
	})
	if err := sl.GetConfigManager().SetDisabled("doomed"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(workspace, "skills", "doomed", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name   string
		filter []string
		skill  string
		want   bool
	}{
		{name: "folder-name", skill: "alpha", want: true},
		{name: "frontmatter-name", skill: "aliased", want: true},
		{name: "unfiltered-allows", filter: []string{"alpha"}, skill: "alpha", want: true},
		{name: "filtered-out", filter: []string{"aliased"}, skill: "alpha", want: false},
		{name: "deleted-body", skill: "doomed", want: false},
		{name: "unknown", skill: "ghost", want: false},
		{name: "traversal", skill: "..", want: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sl.SkillResolvable(tc.filter, tc.skill))
		})
	}
}

func TestSkillsLoader_SkillVisible(t *testing.T) {
	sl := newSummaryLoader(t, "alpha", "beta")
	if err := sl.GetConfigManager().SetDisabled("beta"); err != nil {
		t.Fatal(err)
	}

	testcases := []struct {
		name   string
		filter []string
		skill  string
		want   bool
	}{
		{name: "no-filter-enabled", filter: nil, skill: "alpha", want: true},
		{name: "filter-allows", filter: []string{"alpha", "beta"}, skill: "alpha", want: true},
		{name: "filter-excludes", filter: []string{"beta"}, skill: "alpha", want: false},
		{name: "filter-trims-whitespace", filter: []string{" alpha "}, skill: "alpha", want: true},
		{name: "disabled", filter: nil, skill: "beta", want: false},
		{name: "disabled-even-when-allowed", filter: []string{"beta"}, skill: "beta", want: false},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sl.SkillVisible(tc.filter, tc.skill))
		})
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
