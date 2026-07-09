package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildHarnessContext(t *testing.T) {
	// Save current working directory
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	// Create temporary directory
	tempDir := t.TempDir()

	// Change to temporary directory
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalCwd)
	}()

	// Create some files and directories
	if err := os.Mkdir(filepath.Join(tempDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file1.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("My custom agent rules"), 0644); err != nil {
		t.Fatalf("failed to create AGENTS.md: %v", err)
	}

	// Build context
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Verify contents
	if !strings.Contains(ctxStr, "## Harness Module") {
		t.Errorf("expected output to contain Harness Module heading")
	}
	if !strings.Contains(ctxStr, "Current Directory:") {
		t.Errorf("expected output to contain Current Directory section")
	}
	if !strings.Contains(ctxStr, "- subdir/") {
		t.Errorf("expected output to contain subdir/")
	}
	if !strings.Contains(ctxStr, "- file1.txt") {
		t.Errorf("expected output to contain file1.txt")
	}
	if !strings.Contains(ctxStr, "### AGENTS.md") {
		t.Errorf("expected output to contain AGENTS.md heading")
	}
	if !strings.Contains(ctxStr, "My custom agent rules") {
		t.Errorf("expected output to contain AGENTS.md content")
	}
}

func TestBuildHarnessContext_DirEntriesLimit(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create 150 files with predictable sorted names: file_0001.txt .. file_0150.txt
	for i := 1; i <= 150; i++ {
		fname := filepath.Join(tempDir, fmt.Sprintf("file_%04d.txt", i))
		if err := os.WriteFile(fname, []byte("x"), 0644); err != nil {
			t.Fatalf("failed to create file %d: %v", i, err)
		}
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Should contain truncation notice
	if !strings.Contains(ctxStr, "... and 50 more") {
		t.Errorf("expected output to contain '... and 50 more'")
	}

	// File #1 (file_0001.txt) should be present — it's in the first 100
	if !strings.Contains(ctxStr, "file_0001.txt") {
		t.Errorf("expected file_0001.txt to be listed (within first 100)")
	}

	// File #130 (file_0130.txt) is beyond the 100-entry limit and should NOT appear
	if strings.Contains(ctxStr, "file_0130.txt") {
		t.Errorf("file_0130.txt should NOT appear in output (beyond 100-entry limit)")
	}
}

func TestBuildHarnessContext_AgentsMdTruncation(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create an AGENTS.md larger than maxAgentsFileBytes (16384)
	bigContent := strings.Repeat("A", maxAgentsFileBytes+5000)
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(bigContent), 0644); err != nil {
		t.Fatalf("failed to create AGENTS.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Must contain truncation marker
	if !strings.Contains(ctxStr, "[AGENTS.md truncated]") {
		t.Errorf("expected output to contain '[AGENTS.md truncated]'")
	}

	// The content portion after "### AGENTS.md\n\n" should be at most maxAgentsFileBytes
	// plus the truncation notice. The full output should be bounded.
	// Extract the AGENTS.md section to check its size.
	idx := strings.Index(ctxStr, "### AGENTS.md\n\n")
	if idx < 0 {
		t.Fatal("AGENTS.md section not found")
	}
	agentsSection := ctxStr[idx+len("### AGENTS.md\n\n"):]
	// agentsSection should be <= maxAgentsFileBytes + len("\n\n... [AGENTS.md truncated]\n")
	maxAllowed := maxAgentsFileBytes + len("\n\n... [AGENTS.md truncated]\n")
	if len(agentsSection) > maxAllowed {
		t.Errorf("AGENTS.md section too large: got %d bytes, max allowed %d", len(agentsSection), maxAllowed)
	}

	// Verify the embedded content is exactly maxAgentsFileBytes of 'A's
	embedded := agentsSection[:maxAgentsFileBytes]
	if embedded != strings.Repeat("A", maxAgentsFileBytes) {
		t.Errorf("embedded content mismatch")
	}
}

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"short string", "hello", 100, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"truncate ASCII", "hello world", 5, "hello"},
		{
			"truncate at valid rune boundary (multi-byte UTF-8)",
			// "é" is 2 bytes (0xC3 0xA9), "a" is 1 byte
			// "éaé" = 2+1+2 = 5 bytes; truncate at 3 should give "éa" (3 bytes)
			"\u00e9a\u00e9", 3, "\u00e9a",
		},
		{
			"truncate avoids splitting 2-byte rune",
			// "é" = 2 bytes; truncate at 1 should give "" (back off the continuation byte)
			"\u00e9", 1, "",
		},
		{
			"truncate avoids splitting 3-byte rune (emoji-ish)",
			// "€" is 3 bytes (0xE2 0x82 0xAC)
			"\u20ac", 2, "",
		},
		{
			"truncate with 4-byte rune",
			// "𐍈" (U+10348) is 4 bytes; total string = 5+4+5 = 14 bytes
			// truncate at 7: walk back from idx=7 past continuation bytes → idx=5 → "hello"
			"hello" + "\U00010348" + "world", 7, "hello",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.s, tt.max)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d) = %q (len=%d), want %q (len=%d)", tt.s, tt.max, got, len(got), tt.want, len(tt.want))
			}
			// Also verify the result is valid UTF-8
			if !utf8.ValidString(got) {
				t.Errorf("truncateUTF8(%q, %d) returned invalid UTF-8: %q", tt.s, tt.max, got)
			}
		})
	}
}

func TestBuildHarnessContext_Skills(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Set HOME env var to point to a temporary home directory inside tempDir
	tempHome := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(tempHome, 0755); err != nil {
		t.Fatalf("failed to create temp home: %v", err)
	}
	t.Setenv("HOME", tempHome)
	t.Setenv("USERPROFILE", tempHome) // Windows compatibility

	// 1. Create skill under .agents/skills
	skill1Dir := filepath.Join(tempDir, ".agents", "skills", "skill-one")
	if err := os.MkdirAll(skill1Dir, 0755); err != nil {
		t.Fatalf("failed to create .agents/skills/skill-one: %v", err)
	}
	skill1Content := "---\nname: skill-one-parsed\ndescription: Description for skill one\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0644); err != nil {
		t.Fatalf("failed to write skill-one SKILL.md: %v", err)
	}

	// 2. Create skill under .lele/skills
	skill2Dir := filepath.Join(tempDir, ".lele", "skills", "skill-two")
	if err := os.MkdirAll(skill2Dir, 0755); err != nil {
		t.Fatalf("failed to create .lele/skills/skill-two: %v", err)
	}
	skill2Content := "---\nname: \"skill-two-parsed\"\ndescription: \"Description for skill two\"\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skill2Dir, "skill.md"), []byte(skill2Content), 0644); err != nil {
		t.Fatalf("failed to write skill-two skill.md: %v", err)
	}

	// 3. Create skill under ~/.agents/skill/plan
	skill3Dir := filepath.Join(tempHome, ".agents", "skill", "plan", "skill-three")
	if err := os.MkdirAll(skill3Dir, 0755); err != nil {
		t.Fatalf("failed to create ~/.agents/skill/plan/skill-three: %v", err)
	}
	skill3Content := "---\nname: skill-three-parsed\ndescription: Description for skill three\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skill3Dir, "SKILL.md"), []byte(skill3Content), 0644); err != nil {
		t.Fatalf("failed to write skill-three SKILL.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Verify all three skills are present in the output
	if !strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output to contain Available Skills heading")
	}

	expectedSkill1 := "- **skill-one-parsed** (source: `.agents/skills`): Description for skill one"
	if !strings.Contains(ctxStr, expectedSkill1) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expectedSkill1, ctxStr)
	}

	expectedSkill2 := "- **skill-two-parsed** (source: `.lele/skills`): Description for skill two"
	if !strings.Contains(ctxStr, expectedSkill2) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expectedSkill2, ctxStr)
	}

	expectedSkill3 := "- **skill-three-parsed** (source: `~/.agents/skill/plan`): Description for skill three"
	if !strings.Contains(ctxStr, expectedSkill3) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expectedSkill3, ctxStr)
	}
}

func TestParseSkillMetadata(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectedName string
		expectedDesc string
	}{
		{
			name:         "no description key",
			input:        "---\nname: test\ndesc: val\n---",
			expectedName: "test",
			expectedDesc: "",
		},
		{
			name:         "description with value",
			input:        "---\nname: test\ndescription: Hello World\n---",
			expectedName: "test",
			expectedDesc: "Hello World",
		},
		{
			name:         "quoted values",
			input:        "---\nname: \"quoted\"\ndescription: 'single quoted'\n---",
			expectedName: "quoted",
			expectedDesc: "single quoted",
		},
		{
			name:         "no name field",
			input:        "---\ndescription: no name\n---",
			expectedName: "",
			expectedDesc: "no name",
		},
		{
			name:         "value with --- inside",
			input:        "---\nname: test\ndescription: \"has --- inside\"\n---",
			expectedName: "test",
			expectedDesc: "has --- inside",
		},
		{
			name:         "no newline after opening delimiter",
			input:        "---name: no newline\n---",
			expectedName: "",
			expectedDesc: "",
		},
		{
			name:         "no closing delimiter",
			input:        "---\nname: test\n",
			expectedName: "",
			expectedDesc: "",
		},
		{
			name:         "leading newline before opening delimiter",
			input:        "\n---\nname: test\n---",
			expectedName: "",
			expectedDesc: "",
		},
		{
			name:         "folded block scalar",
			input:        "---\nname: test\ndescription: >\n  long desc\n  continues\n---",
			expectedName: "test",
			expectedDesc: "long desc continues",
		},
		{
			name:         "literal block scalar",
			input:        "---\nname: test\ndescription: |\n  line1\n  line2\n---",
			expectedName: "test",
			expectedDesc: "line1\nline2",
		},
		{
			name:         "empty input",
			input:        "",
			expectedName: "",
			expectedDesc: "",
		},
		{
			name:         "no frontmatter at all",
			input:        "no frontmatter at all",
			expectedName: "",
			expectedDesc: "",
		},
		{
			name:         "value containing hash is not a comment",
			input:        "---\nname: test\ndescription: value with # comment\n---",
			expectedName: "test",
			expectedDesc: "value with # comment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := parseSkillMetadata(tt.input)
			if meta.Name != tt.expectedName {
				t.Errorf("Name: got %q, want %q", meta.Name, tt.expectedName)
			}
			if meta.Description != tt.expectedDesc {
				t.Errorf("Description: got %q, want %q", meta.Description, tt.expectedDesc)
			}
		})
	}
}

func TestBuildHarnessContext_SkillWithoutDescription(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create a skill with frontmatter that has name but NO description field
	skillDir := filepath.Join(tempDir, ".agents", "skills", "no-desc-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	skillContent := "---\nname: my-skill\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	if !strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output to contain Available Skills heading")
	}

	expected := "- **my-skill** (source: `.agents/skills`): No description provided."
	if !strings.Contains(ctxStr, expected) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expected, ctxStr)
	}
}

func TestBuildHarnessContext_SkillFrontmatterNameOverridesDir(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create a skill directory named "dir-name" but with "name: parsed-name" in SKILL.md
	skillDir := filepath.Join(tempDir, ".agents", "skills", "dir-name")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("failed to create skill dir: %v", err)
	}
	skillContent := "---\nname: parsed-name\ndescription: Overridden by frontmatter\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	if !strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output to contain Available Skills heading")
	}

	// Should use parsed-name from frontmatter, NOT dir-name from directory
	expected := "- **parsed-name** (source: `.agents/skills`): Overridden by frontmatter"
	if !strings.Contains(ctxStr, expected) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expected, ctxStr)
	}

	// Should NOT contain the directory name as a skill name
	if strings.Contains(ctxStr, "- **dir-name**") {
		t.Errorf("output should NOT contain directory name 'dir-name' as skill name")
	}
}

func TestBuildHarnessContext_NoSkillsDir(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// No skills directories created at all
	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	if strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output NOT to contain '### Available Skills' when no skills dirs exist")
	}
}

func TestBuildHarnessContext_SkillsWithFiles(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create .agents/skills/ with a FILE inside (not a directory)
	skillsDir := filepath.Join(tempDir, ".agents", "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create .agents/skills: %v", err)
	}
	// This should be ignored by scanSkillsDir since it's a file, not a directory
	if err := os.WriteFile(filepath.Join(skillsDir, "not-a-skill.md"), []byte("fake skill"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	// Should not crash and should not list any skills
	if strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output NOT to contain '### Available Skills' when only files (not dirs) exist in skills dir")
	}

	if strings.Contains(ctxStr, "not-a-skill") {
		t.Errorf("expected file entry 'not-a-skill' to be ignored")
	}
}

func TestBuildHarnessContext_DuplicateSkills(t *testing.T) {
	originalCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get original cwd: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to change cwd: %v", err)
	}
	defer func() { _ = os.Chdir(originalCwd) }()

	// Create the same skill name in .agents/skills AND .lele/skills
	// .agents/skills has priority (scanned first)

	// 1. .agents/skills/my-skill
	skill1Dir := filepath.Join(tempDir, ".agents", "skills", "my-skill")
	if err := os.MkdirAll(skill1Dir, 0755); err != nil {
		t.Fatalf("failed to create .agents/skills/my-skill: %v", err)
	}
	skill1Content := "---\nname: my-skill\ndescription: From agents\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(skill1Content), 0644); err != nil {
		t.Fatalf("failed to write skill1 SKILL.md: %v", err)
	}

	// 2. .lele/skills/my-skill (same name, different description)
	skill2Dir := filepath.Join(tempDir, ".lele", "skills", "my-skill")
	if err := os.MkdirAll(skill2Dir, 0755); err != nil {
		t.Fatalf("failed to create .lele/skills/my-skill: %v", err)
	}
	skill2Content := "---\nname: my-skill\ndescription: From lele\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(skill2Content), 0644); err != nil {
		t.Fatalf("failed to write skill2 SKILL.md: %v", err)
	}

	ctxStr, err := BuildHarnessContext()
	if err != nil {
		t.Fatalf("BuildHarnessContext failed: %v", err)
	}

	if !strings.Contains(ctxStr, "### Available Skills") {
		t.Errorf("expected output to contain Available Skills heading")
	}

	// Should appear with .agents/skills source (first priority) and its description
	expected := "- **my-skill** (source: `.agents/skills`): From agents"
	if !strings.Contains(ctxStr, expected) {
		t.Errorf("expected output to contain:\n%s\nGot output:\n%s", expected, ctxStr)
	}

	// Should NOT appear with .lele/skills source (deduplication)
	unexpected := "- **my-skill** (source: `.lele/skills`)"
	if strings.Contains(ctxStr, unexpected) {
		t.Errorf("expected duplicate with .lele/skills source to be deduplicated, but found:\n%s", unexpected)
	}

	// Count occurrences of "**my-skill**" to verify it appears exactly once
	count := strings.Count(ctxStr, "**my-skill**")
	if count != 1 {
		t.Errorf("expected 'my-skill' to appear exactly once, got %d occurrences", count)
	}
}
