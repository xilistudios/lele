package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
)

// --- skills.go additional coverage ---

func TestFormatSkillItem(t *testing.T) {
	enabled := formatSkillItem("my-skill", "a description", true, "github")
	if !strings.HasPrefix(enabled, "●") {
		t.Errorf("expected enabled dot, got %q", enabled)
	}
	if !strings.Contains(enabled, "my-skill") || !strings.Contains(enabled, "a description") || !strings.Contains(enabled, "[github]") {
		t.Errorf("formatSkillItem enabled = %q", enabled)
	}

	disabled := formatSkillItem("ns", "desc", false, "")
	if !strings.HasPrefix(disabled, "○") {
		t.Errorf("expected disabled dot, got %q", disabled)
	}
	if strings.Contains(disabled, "[") {
		t.Errorf("expected no source tag when empty, got %q", disabled)
	}
}

func TestFormatSkillItemLongDescription(t *testing.T) {
	long := strings.Repeat("x", 80)
	out := formatSkillItem("s", long, true, "")
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation marker, got %q", out)
	}
}

func TestFormatPickerItem(t *testing.T) {
	sel := formatPickerItem("a", "d", true)
	if !strings.Contains(sel, "[x]") {
		t.Errorf("expected [x] for selected, got %q", sel)
	}
	unsel := formatPickerItem("b", "e", false)
	if !strings.Contains(unsel, "[ ]") {
		t.Errorf("expected [ ] for unselected, got %q", unsel)
	}
	long := formatPickerItem("c", strings.Repeat("y", 60), false)
	if !strings.Contains(long, "...") {
		t.Errorf("expected truncation for long description, got %q", long)
	}
}

func TestSortSkillsByName(t *testing.T) {
	skills := []channels.ScannedSkill{
		{Name: "Zeta"},
		{Name: "alpha"},
		{Name: "Beta"},
	}
	sortSkillsByName(skills)
	if skills[0].Name != "alpha" || skills[1].Name != "Beta" || skills[2].Name != "Zeta" {
		t.Errorf("sortSkillsByName order = %v", []string{skills[0].Name, skills[1].Name, skills[2].Name})
	}
}

func TestCleanRepoURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://github.com/owner/repo", "owner/repo"},
		{"http://github.com/owner/repo/", "owner/repo"},
		{"github.com/owner/repo", "owner/repo"},
		{"owner/repo", "owner/repo"},
		{"owner/repo/deep/path", "owner/repo"},
		{"   https://github.com/owner/repo  ", "owner/repo"},
		{"just-one", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := cleanRepoURL(tt.in); got != tt.want {
			t.Errorf("cleanRepoURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}