package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- BuildHarnessContext edge branches -------------------------------

func TestBuildHarnessContext_EmptyDir(t *testing.T) {
	temp := t.TempDir()
	// Put HOME outside 'temp' so 'temp' stays truly empty.
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	chdir(t, temp)

	// Create NO files or directories and no skills dirs.
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}
	if !strings.Contains(ctxStr, "No files or directories found.") {
		t.Errorf("expected 'No files or directories found.' in output, got:\n%s", ctxStr)
	}
}

func TestBuildHarnessContext_SkillsLimit(t *testing.T) {
	temp := t.TempDir()
	setTempHome(t, temp)
	chdir(t, temp)

	// Create 55 skill dirs (maxHarnessSkills = 50).
	const total = maxHarnessSkills + 5
	base := filepath.Join(temp, ".agents", "skills")
	for i := 0; i < total; i++ {
		d := filepath.Join(base, fmt.Sprintf("skill-%02d", i))
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("failed to create skill dir %d: %v", i, err)
		}
		content := fmt.Sprintf("---\nname: skill-%02d\ndescription: desc %d\n---", i, i)
		if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(content), 0644); err != nil {
			t.Fatalf("failed to write SKILL.md %d: %v", i, err)
		}
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	if !strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected Available Skills heading")
	}
	if !strings.Contains(ctxStr, "... and more (limited to 50 skills)") {
		t.Errorf("expected skills truncation notice, got:\n%s", ctxStr)
	}
	// First skill should be present.
	if !strings.Contains(ctxStr, "skill-00") {
		t.Errorf("expected skill-00 to be listed")
	}
	// Beyond the limit (skill-50) should not be listed.
	if strings.Contains(ctxStr, "skill-50") {
		t.Errorf("skill-50 should not be listed (beyond limit)")
	}
}

func TestBuildHarnessContext_LowercaseAgentsMd(t *testing.T) {
	temp := t.TempDir()
	setTempHome(t, temp)
	chdir(t, temp)

	if err := os.WriteFile(filepath.Join(temp, "agents.md"), []byte("lowercase rules"), 0644); err != nil {
		t.Fatal(err)
	}
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}
	if !strings.Contains(ctxStr, "### AGENTS.md") || !strings.Contains(ctxStr, "lowercase rules") {
		t.Errorf("expected lowercase agents.md content to be included, got:\n%s", ctxStr)
	}
}

func TestBuildHarnessContext_SkillMdOnly(t *testing.T) {
	// A skill dir containing only skill.md (lowercase) should be detected.
	temp := t.TempDir()
	setTempHome(t, temp)
	chdir(t, temp)

	d := filepath.Join(temp, ".agents", "skills", "low")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "skill.md"), []byte("---\nname: low\ndescription: lower file\n---"), 0644); err != nil {
		t.Fatal(err)
	}
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}
	if !strings.Contains(ctxStr, "- **low** (source: `.agents/skills`): lower file") {
		t.Errorf("expected lowercase skill.md to be parsed, got:\n%s", ctxStr)
	}
}

func TestBuildHarnessContext_NoUsableSkillMd(t *testing.T) {
	// A skill dir with neither SKILL.md nor skill.md should be ignored.
	temp := t.TempDir()
	setTempHome(t, temp)
	chdir(t, temp)

	d := filepath.Join(temp, ".agents", "skills", "empty-dir")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "README.md"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}
	if strings.Contains(ctxStr, "Available Skills") {
		t.Errorf("expected no skills heading when no skill.md files exist")
	}
}

// --- parseSkillMetadata additional branches ---------------------------

func TestParseSkillMetadata_Additional(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantName     string
		wantDesc     string
	}{
		{
			name:         "literal block keep trailing newline (|+)",
			input:        "---\nname: n\ndescription: |+\n  a\n  b\n---",
			wantName:     "n",
			wantDesc:     "a\nb\n",
		},
		{
			name:         "literal block strip chomp (|-)",
			input:        "---\ndescription: |-\n  a\n  b\n---",
			wantDesc:     "a\nb",
		},
		{
			name:         "folded block keep newline (>+)",
			input:        "---\ndescription: >+\n  a\n  b\n---",
			wantDesc:     "a b\n",
		},
		{
			name:         "folded block with blank paragraph break",
			input:        "---\ndescription: >\n  line1\n\n  line2\n---",
			wantDesc:     "line1\nline2",
		},
		{
			name:         "comment and blank lines in frontmatter",
			input:        "---\n# a comment\n\nname: test\ndescription: desc\n---",
			wantName:     "test",
			wantDesc:     "desc",
		},
		{
			name:         "line without colon is skipped",
			input:        "---\nname: test\njust a line without colon\n---",
			wantName:     "test",
			wantDesc:     "",
		},
		{
			name:         "tab-indented continuation",
			input:        "---\nname: test\ndescription: first\n\tsecond\n---",
			wantName:     "test",
			wantDesc:     "first second",
		},
		{
			name:         "crlf opening delimiter",
			input:        "---\r\nname: crlf\r\ndescription: ok\r\n---",
			wantName:     "crlf",
			wantDesc:     "ok",
		},
		{
			name:         "closing delimiter whitespace then newline",
			input:        "---\nname: test\n---   \n",
			wantName:     "test",
			wantDesc:     "",
		},
		{
			name:         "empty frontmatter body name only",
			input:        "---\nname: x\n---",
			wantName:     "x",
			wantDesc:     "",
		},
		{
			name:         "block scalar for folded default",
			input:        "---\ndescription: >\n  one\n  two\n---",
			wantDesc:     "one two",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := parseSkillMetadata(tt.input)
			if meta.Name != tt.wantName || meta.Description != tt.wantDesc {
				t.Errorf("parseSkillMetadata = {Name:%q Desc:%q}, want {Name:%q Desc:%q}",
					meta.Name, meta.Description, tt.wantName, tt.wantDesc)
			}
		})
	}
}

func TestFoldBlock_BlankLine(t *testing.T) {
	got := foldBlock([]string{"one", "", "two", "three"})
	want := "one\ntwo three"
	if got != want {
		t.Errorf("foldBlock = %q, want %q", got, want)
	}
}

func TestTrimBlockEdges(t *testing.T) {
	got := trimBlockEdges([]string{"", "a", "", "b", ""})
	want := []string{"a", "", "b"}
	if len(got) != len(want) {
		t.Fatalf("trimBlockEdges length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("trimBlockEdges[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// All blank -> empty.
	if len(trimBlockEdges([]string{"", "", ""})) != 0 {
		t.Errorf("expected empty result for all-blank input")
	}
	if len(trimBlockEdges(nil)) != 0 {
		t.Errorf("expected empty result for nil input")
	}
}

// setTempHome sets HOME/USERPROFILE to a temp dir and returns it.
func setTempHome(t *testing.T, base string) string {
	t.Helper()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatalf("failed to create temp home: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}