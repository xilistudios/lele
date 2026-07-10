package context

import (
	"testing"
)

func TestParseSkillMetadata_Basic(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\n---")
	if meta.Name != "test" {
		t.Errorf("Name = %q, want %q", meta.Name, "test")
	}
}

func TestParseSkillMetadata_DashesInValue(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\ndescription: \"has --- inside\"\n---")
	want := "has --- inside"
	if meta.Description != want {
		t.Errorf("Description = %q, want %q", meta.Description, want)
	}
}

func TestParseSkillMetadata_FoldedBlock(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\ndescription: >\n  long desc\n  continues\n---")
	want := "long desc continues"
	if meta.Description != want {
		t.Errorf("Description = %q, want %q", meta.Description, want)
	}
}

func TestParseSkillMetadata_LiteralBlock(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\ndescription: |\n  line1\n  line2\n---")
	want := "line1\nline2"
	if meta.Description != want {
		t.Errorf("Description = %q, want %q", meta.Description, want)
	}
}

func TestParseSkillMetadata_NoNewlineAfterOpening(t *testing.T) {
	meta := parseSkillMetadata("---name: test\n---")
	if meta.Name != "" || meta.Description != "" {
		t.Errorf("expected empty metadata for no newline after opening, got Name=%q Description=%q", meta.Name, meta.Description)
	}
}

func TestParseSkillMetadata_NoClosingDelimiter(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\n")
	if meta.Name != "" || meta.Description != "" {
		t.Errorf("expected empty metadata for no closing ---, got Name=%q Description=%q", meta.Name, meta.Description)
	}
}

func TestParseSkillMetadata_NewlineBeforeOpening(t *testing.T) {
	meta := parseSkillMetadata("\n---\nname: test\n---")
	if meta.Name != "" || meta.Description != "" {
		t.Errorf("expected empty metadata for newline before opening, got Name=%q Description=%q", meta.Name, meta.Description)
	}
}

func TestParseSkillMetadata_DashesInMiddleOfContent(t *testing.T) {
	// --- inside a value (not on its own line) should not close frontmatter
	input := "---\nname: test\ndescription: use --- for emphasis\n---"
	meta := parseSkillMetadata(input)
	want := "use --- for emphasis"
	if meta.Description != want {
		t.Errorf("Description = %q, want %q", meta.Description, want)
	}
	if meta.Name != "test" {
		t.Errorf("Name = %q, want %q", meta.Name, "test")
	}
}

func TestParseSkillMetadata_ClosingDashesWithTrailingSpaces(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\n---   \n")
	if meta.Name != "test" {
		t.Errorf("Name = %q, want %q", meta.Name, "test")
	}
}

func TestParseSkillMetadata_QuotedValues(t *testing.T) {
	meta := parseSkillMetadata("---\nname: \"quoted name\"\ndescription: 'quoted desc'\n---")
	if meta.Name != "quoted name" {
		t.Errorf("Name = %q, want %q", meta.Name, "quoted name")
	}
	if meta.Description != "quoted desc" {
		t.Errorf("Description = %q, want %q", meta.Description, "quoted desc")
	}
}

func TestParseSkillMetadata_IndentedContinuation(t *testing.T) {
	meta := parseSkillMetadata("---\nname: test\ndescription: short desc\n  and more\n  and even more\n---")
	want := "short desc and more and even more"
	if meta.Description != want {
		t.Errorf("Description = %q, want %q", meta.Description, want)
	}
}

func TestParseSkillMetadata_EmptyContent(t *testing.T) {
	meta := parseSkillMetadata("")
	if meta.Name != "" || meta.Description != "" {
		t.Errorf("expected empty metadata for empty content, got Name=%q Description=%q", meta.Name, meta.Description)
	}
}

func TestParseSkillMetadata_OnlyDelimiters(t *testing.T) {
	meta := parseSkillMetadata("---\n---")
	if meta.Name != "" || meta.Description != "" {
		t.Errorf("expected empty metadata for only delimiters, got Name=%q Description=%q", meta.Name, meta.Description)
	}
}
