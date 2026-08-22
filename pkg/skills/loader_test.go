package skills

import (
	"os"
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
			// Extract frontmatter
			frontmatter := sl.extractFrontmatter(tc.content)
			assert.NotEmpty(t, frontmatter, "Frontmatter should be extracted for %s line endings", tc.lineEndingType)

			// Parse YAML to get name and description (parseSimpleYAML now handles all line ending types)
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

// ---- Additional tests for uncovered loader functions ----

func TestNewSkillsLoader(t *testing.T) {
	tmp := t.TempDir()
	// With a valid workspace, config manager should be created.
	sl := NewSkillsLoader(tmp, "", "")
	if sl == nil {
		t.Fatal("expected non-nil loader")
	}
	if sl.workspace != tmp {
		t.Errorf("expected workspace %q, got %q", tmp, sl.workspace)
	}
	if sl.workspaceSkills != tmp+"/skills" {
		t.Errorf("expected workspaceSkills %q", tmp+"/skills")
	}
	if sl.configMgr == nil {
		t.Error("expected config manager to be set for valid workspace")
	}

	// Empty workspace -> no config manager.
	sl2 := NewSkillsLoader("", "", "")
	if sl2 == nil {
		t.Fatal("expected non-nil loader")
	}
	if sl2.configMgr != nil {
		t.Error("expected nil config manager for empty workspace")
	}

	// Invalid workspace config -> warns, no config manager, no panic.
	badWorkspace := t.TempDir()
	if err := os.MkdirAll(badWorkspace+"/.lele", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badWorkspace+"/.lele/workspace.json", []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	sl3 := NewSkillsLoader(badWorkspace, "", "")
	if sl3 == nil {
		t.Fatal("expected non-nil loader")
	}
	if sl3.configMgr != nil {
		t.Error("expected nil config manager for invalid workspace config")
	}
}

func TestSetGetConfigManager(t *testing.T) {
	tmp := t.TempDir()
	mgr, err := NewWorkspaceConfigManager(tmp)
	if err != nil {
		t.Fatal(err)
	}
	sl := NewSkillsLoader(tmp, "", "")
	sl.SetConfigManager(mgr)
	if sl.GetConfigManager() != mgr {
		t.Error("expected GetConfigManager to return the injected manager")
	}
}

func TestIsSkillEnabled(t *testing.T) {
	tmp := t.TempDir()
	mgr, err := NewWorkspaceConfigManager(tmp)
	if err != nil {
		t.Fatal(err)
	}
	_ = mgr.SetDisabled("disabled-skill")

	// Without config manager -> always enabled.
	slNoCfg := &SkillsLoader{}
	if !slNoCfg.isSkillEnabled("anything") {
		t.Error("expected enabled when no config manager")
	}

	sl := &SkillsLoader{configMgr: mgr}
	if sl.isSkillEnabled("disabled-skill") {
		t.Error("expected disabled-skill to be disabled")
	}
	if !sl.isSkillEnabled("other-skill") {
		t.Error("expected other-skill to be enabled")
	}
}

func TestLoadSkill(t *testing.T) {
	ws := t.TempDir()
	global := t.TempDir()
	builtin := t.TempDir()

	// Create a skill in each source with distinct content.
	wsSkill(t, ws, "ws-skill", "name: ws-skill\n---\n# WS")
	writeSkill(t, global, "global-skill", "name: global-skill\n---\n# Global")
	writeSkill(t, builtin, "builtin-skill", "name: builtin-skill\n---\n# Builtin")
	// Same name in workspace and builtin: workspace wins.
	wsSkill(t, ws, "shared", "name: shared\n---\n# From WS")
	writeSkill(t, builtin, "shared", "name: shared\n---\n# From Builtin")

	sl := NewSkillsLoader(ws, global, builtin)

	// Workspace takes priority.
	content, ok := sl.LoadSkill("ws-skill")
	if !ok || !contains(content, "# WS") {
		t.Errorf("expected workspace skill content, got ok=%v content=%q", ok, content)
	}
	// Frontmatter should be stripped.
	if contains(content, "name: ws-skill") {
		t.Errorf("expected frontmatter stripped, got %q", content)
	}

	// Global fallback.
	content, ok = sl.LoadSkill("global-skill")
	if !ok || !contains(content, "# Global") {
		t.Errorf("expected global skill content, got ok=%v content=%q", ok, content)
	}

	// Builtin fallback.
	content, ok = sl.LoadSkill("builtin-skill")
	if !ok || !contains(content, "# Builtin") {
		t.Errorf("expected builtin skill content, got ok=%v content=%q", ok, content)
	}

	// Workspace overrides builtin for same name.
	content, ok = sl.LoadSkill("shared")
	if !ok || !contains(content, "# From WS") {
		t.Errorf("expected workspace to win, got ok=%v content=%q", ok, content)
	}

	// Not found.
	if _, ok := sl.LoadSkill("does-not-exist"); ok {
		t.Error("expected not found for missing skill")
	}
}

// writeSkill creates dir/name/SKILL.md with the given content.
// For workspace skills, pass dir = "<workspace>/skills".
func writeSkill(t *testing.T, dir string, name string, frontmatterYAML string) {
	t.Helper()
	skillDir := dir + "/" + name
	if err := mkdirAll(skillDir); err != nil {
		t.Fatal(err)
	}
	content := "---\n" + frontmatterYAML + "\n"
	if err := writeFile(skillDir+"/SKILL.md", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// wsSkill writes a workspace skill (writerunder <ws>/skills/<name>).
func wsSkill(t *testing.T, ws string, name string, frontmatterYAML string) {
	t.Helper()
	writeSkill(t, ws+"/skills", name, frontmatterYAML)
}

func TestLoadSkillsForContext(t *testing.T) {
	ws := t.TempDir()
	wsSkill(t, ws, "skill-a", "name: skill-a\ndescription: Skill A\n---\n# A")
	wsSkill(t, ws, "skill-b", "name: skill-b\n---\n# B")

	mgr, _ := NewWorkspaceConfigManager(ws)
	sl := NewSkillsLoader(ws, "", "")

	// Empty list -> empty string.
	if got := sl.LoadSkillsForContext(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	if got := sl.LoadSkillsForContext([]string{}); got != "" {
		t.Errorf("expected empty for empty list, got %q", got)
	}

	// Loads two skills.
	out := sl.LoadSkillsForContext([]string{"skill-a", "skill-b"})
	if !contains(out, "### Skill: skill-a") || !contains(out, "# A") {
		t.Errorf("expected skill-a in context, got %q", out)
	}
	if !contains(out, "### Skill: skill-b") || !contains(out, "# B") {
		t.Errorf("expected skill-b in context, got %q", out)
	}
	if !contains(out, "---") {
		t.Errorf("expected separator in context, got %q", out)
	}

	// Missing skills are skipped silently.
	out = sl.LoadSkillsForContext([]string{"skill-a", "missing"})
	if !contains(out, "skill-a") {
		t.Errorf("expected skill-a, got %q", out)
	}
	if contains(out, "missing") {
		t.Errorf("missing skill should not appear, got %q", out)
	}

	// Disabled skills skipped.
	_ = mgr.SetDisabled("skill-a")
	sl.SetConfigManager(mgr)
	out = sl.LoadSkillsForContext([]string{"skill-a", "skill-b"})
	if contains(out, "skill-a") {
		t.Errorf("disabled skill should be skipped, got %q", out)
	}
	if !contains(out, "skill-b") {
		t.Errorf("enabled skill should be included, got %q", out)
	}
}

func TestBuildSkillsSummary(t *testing.T) {
	ws := t.TempDir()
	wsSkill(t, ws, "enabled-skill", "name: enabled-skill\ndescription: <b>Enabled</b> & useful\n---\n# E")
	wsSkill(t, ws, "disabled-skill", "name: disabled-skill\ndescription: Disabled\n---\n# D")

	mgr, _ := NewWorkspaceConfigManager(ws)
	_ = mgr.SetDisabled("disabled-skill")
	sl := NewSkillsLoader(ws, "", "")
	sl.SetConfigManager(mgr)

	summary := sl.BuildSkillsSummary()
	if !contains(summary, "<skills>") {
		t.Errorf("expected <skills> open tag, got %q", summary)
	}
	if !contains(summary, "</skills>") {
		t.Errorf("expected </skills> close tag, got %q", summary)
	}
	if !contains(summary, "enabled-skill") {
		t.Errorf("expected enabled-skill in summary, got %q", summary)
	}
	if contains(summary, "disabled-skill") {
		t.Errorf("disabled skill should not appear in summary, got %q", summary)
	}
	// XML escaping applied.
	if !contains(summary, "&lt;b&gt;") || !contains(summary, "&amp;") {
		t.Errorf("expected XML escaping in summary, got %q", summary)
	}

	// Empty when no skills.
	emptySl := NewSkillsLoader("", "", "")
	if got := emptySl.BuildSkillsSummary(); got != "" {
		t.Errorf("expected empty summary, got %q", got)
	}
}

func TestBuildSkillsSummary_SkipsInvalidSkillsAndNonDir(t *testing.T) {
	ws := t.TempDir()
	// A valid skill.
	wsSkill(t, ws, "good-skill", "name: good-skill\ndescription: desc\n---\n# Good")
	// A directory without SKILL.md (not a skill).
	_ = mkdirAll(ws + "/not-a-skill")
	// A file (not a dir) in the skills folder.
	_ = mkdirAll(ws + "/skills")
	_ = writeFile(ws+"/skills/file.txt", []byte("x"), 0644)

	sl := NewSkillsLoader(ws, "", "")
	summary := sl.BuildSkillsSummary()
	if !contains(summary, "good-skill") {
		t.Errorf("expected good-skill in summary, got %q", summary)
	}
	if contains(summary, "not-a-skill") {
		t.Errorf("not-a-skill should be excluded, got %q", summary)
	}
}

func TestListSkills_SourcesAndOverride(t *testing.T) {
	ws := t.TempDir()
	global := t.TempDir()
	builtin := t.TempDir()

	// A skill only in workspace.
	wsSkill(t, ws, "from-ws", "name: from-ws\ndescription: ws only\n---\n# WS")
	// A skill only in global.
	writeSkill(t, global, "from-global", "name: from-global\ndescription: global only\n---\n# G")
	// A skill only in builtin.
	writeSkill(t, builtin, "from-builtin", "name: from-builtin\ndescription: builtin only\n---\n# B")
	// Same name in ws + global + builtin -> workspace wins.
	wsSkill(t, ws, "override", "name: override\ndescription: WS version\n---\n# WS")
	writeSkill(t, global, "override", "name: override\ndescription: Global version\n---\n# G")
	writeSkill(t, builtin, "override", "name: override\ndescription: Builtin version\n---\n# B")
	// Same name in global + builtin -> global wins.
	writeSkill(t, global, "global-over-builtin", "name: global-over-builtin\ndescription: global\n---\n# G")
	writeSkill(t, builtin, "global-over-builtin", "name: global-over-builtin\ndescription: builtin\n---\n# B")

	sl := NewSkillsLoader(ws, global, builtin)
	skills := sl.ListSkills()

	byName := map[string]SkillInfo{}
	for _, s := range skills {
		byName[s.Name] = s
	}

	check := func(name string, source string) {
		t.Helper()
		s, ok := byName[name]
		if !ok {
			t.Errorf("expected skill %q in list", name)
			return
		}
		if s.Source != source {
			t.Errorf("skill %q: expected source %q, got %q", name, source, s.Source)
		}
	}
	check("from-ws", "workspace")
	check("from-global", "global")
	check("from-builtin", "builtin")
	check("override", "workspace")
	check("global-over-builtin", "global")
}

func TestListSkills_OverrideCaseSensitivity(t *testing.T) {
	// Global vs builtin: global should override builtin only when both exist.
	global := t.TempDir()
	builtin := t.TempDir()
	writeSkill(t, global, "dupe", "name: dupe\ndescription: global\n---\n# G")
	writeSkill(t, builtin, "dupe2", "name: dupe2\ndescription: builtin\n---\n# B")
	// global has dupe; builtin also has dupe -> global wins (already covered). Here ensure builtin-only still listed when no global.

	sl := NewSkillsLoader("", global, builtin)
	skills := sl.ListSkills()
	byName := map[string]SkillInfo{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if _, ok := byName["dupe2"]; !ok {
		t.Error("expected dupe2 (builtin-only) to be listed")
	}
	if s, ok := byName["dupe2"]; ok && s.Source != "builtin" {
		t.Errorf("expected dupe2 source builtin, got %q", s.Source)
	}
}

func TestListSkills_metadataFromFile(t *testing.T) {
	ws := t.TempDir()
	// Directory name differs from metadata name -> metadata name wins.
	wsSkill(t, ws, "dir-name", "name: metadata-name\ndescription: From metadata\n---\n# X")

	sl := NewSkillsLoader(ws, "", "")
	skills := sl.ListSkills()
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	if skills[0].Name != "metadata-name" {
		t.Errorf("expected name from metadata, got %q", skills[0].Name)
	}
	if skills[0].Description != "From metadata" {
		t.Errorf("expected description from metadata, got %q", skills[0].Description)
	}
}

func TestListSkills_invalidSkillSkipped(t *testing.T) {
	ws := t.TempDir()
	// Invalid skill: description missing -> validate fails -> skipped.
	_ = mkdirAll(ws + "/skills/bad-skill")
	_ = writeFile(ws+"/skills/bad-skill/SKILL.md", []byte("---\nname: bad-skill\n---\n# no desc"), 0644)

	sl := NewSkillsLoader(ws, "", "")
	skills := sl.ListSkills()
	if len(skills) != 0 {
		t.Errorf("expected no skills, got %d", len(skills))
	}
}

func TestGetSkillMetadata(t *testing.T) {
	sl := &SkillsLoader{}

	// Missing file -> nil.
	if md := sl.getSkillMetadata("/nonexistent/SKILL.md"); md != nil {
		t.Errorf("expected nil metadata for missing file, got %+v", md)
	}

	// JSON frontmatter.
	dir := t.TempDir()
	_ = writeFile(dir+"/SKILL.md", []byte("---\n{\"name\":\"json-name\",\"description\":\"json desc\"}\n---\n# X"), 0644)
	md := sl.getSkillMetadata(dir + "/SKILL.md")
	if md == nil || md.Name != "json-name" || md.Description != "json desc" {
		t.Errorf("expected JSON metadata, got %+v", md)
	}

	// YAML frontmatter.
	_ = writeFile(dir+"/YAML.md", []byte("---\nname: yaml-name\ndescription: yaml desc\n---\n# X"), 0644)
	md = sl.getSkillMetadata(dir + "/YAML.md")
	if md == nil || md.Name != "yaml-name" || md.Description != "yaml desc" {
		t.Errorf("expected YAML metadata, got %+v", md)
	}

	// No frontmatter -> name from directory.
	_ = mkdirAll(dir + "/metadir")
	_ = writeFile(dir+"/metadir/SKILL.md", []byte("# Just content"), 0644)
	md = sl.getSkillMetadata(dir + "/metadir/SKILL.md")
	if md == nil || md.Name != "metadir" || md.Description != "" {
		t.Errorf("expected dir-name metadata, got %+v", md)
	}

	// Frontmatter without description (name only) -> fallback dir name? Actually parse gives empty name/desc.
	_ = writeFile(dir+"/noname.md", []byte("---\nname: realname\n---\n# X"), 0644)
	md = sl.getSkillMetadata(dir + "/noname.md")
	if md == nil || md.Name != "realname" {
		t.Errorf("expected realname from YAML, got %+v", md)
	}
}

func TestParseSimpleYAML(t *testing.T) {
	sl := &SkillsLoader{}
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"simple", "name: foo\ndescription: bar\n", map[string]string{"name": "foo", "description": "bar"}},
		{"quoted", "name: \"foo\"\ndescription: 'bar'\n", map[string]string{"name": "foo", "description": "bar"}},
		{"comments-and-blank", "# comment\n\nname: foo\n# another\n", map[string]string{"name": "foo"}},
		{"trailing-colon-content", "name: foo:bar\n", map[string]string{"name": "foo:bar"}},
		{"no-colon-line", "just a line\n", map[string]string{}},
		{"windows-endings", "name: foo\r\ndescription: bar\r\n", map[string]string{"name": "foo", "description": "bar"}},
		{"wrap-description-long", "description: This is a very long description that exceeds the maximum length to test the boundary condition of validation\n", map[string]string{"description": "This is a very long description that exceeds the maximum length to test the boundary condition of validation"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sl.parseSimpleYAML(tc.in)
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q: got %q want %q (full map %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestParseSimpleYAML_CREndings(t *testing.T) {
	sl := &SkillsLoader{}
	// Classic Mac \r line endings.
	in := "name: foo\rdescription: bar"
	got := sl.parseSimpleYAML(in)
	if got["name"] != "foo" || got["description"] != "bar" {
		t.Errorf("expected CR endings parsed, got %v", got)
	}
}

func TestExtractFrontmatter_NoFrontmatter(t *testing.T) {
	sl := &SkillsLoader{}
	if got := sl.extractFrontmatter("no frontmatter here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestEscapeXML(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"<tag>", "&lt;tag&gt;"},
		{"a & b", "a &amp; b"},
		{"<a> & <b>", "&lt;a&gt; &amp; &lt;b&gt;"},
	}
	for _, tc := range cases {
		if got := escapeXML(tc.in); got != tc.want {
			t.Errorf("escapeXML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSkillInfoValidate_TooLong(t *testing.T) {
	long := make([]byte, MaxNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	info := SkillInfo{Name: string(long), Description: "ok"}
	err := info.validate()
	if err == nil {
		t.Error("expected error for too-long name")
	}
	if !contains(err.Error(), "exceeds") {
		t.Errorf("expected exceeds error, got %v", err)
	}

	longDesc := make([]byte, MaxDescriptionLength+1)
	for i := range longDesc {
		longDesc[i] = 'a'
	}
	info = SkillInfo{Name: "valid-name", Description: string(longDesc)}
	err = info.validate()
	if err == nil {
		t.Error("expected error for too-long description")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
