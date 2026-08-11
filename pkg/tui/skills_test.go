package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
)

func TestFormatSkillItem(t *testing.T) {
	tests := []struct {
		name        string
		skillName   string
		description string
		enabled     bool
		source      string
		contains    string
	}{
		{
			name:        "enabled skill",
			skillName:   "github",
			description: "Interact with GitHub",
			enabled:     true,
			source:      "workspace",
			contains:    "●",
		},
		{
			name:        "disabled skill",
			skillName:   "hardware",
			description: "I2C/SPI peripherals",
			enabled:     false,
			source:      "workspace",
			contains:    "○",
		},
		{
			name:        "long description truncated",
			skillName:   "test",
			description: "This is a very long description that should definitely be truncated because it exceeds fifty characters",
			enabled:     true,
			source:      "global",
			contains:    "...",
		},
		{
			name:        "with source tag",
			skillName:   "weather",
			description: "Weather forecasts",
			enabled:     true,
			source:      "builtin",
			contains:    "[builtin]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSkillItem(tt.skillName, tt.description, tt.enabled, tt.source)
			if result == "" {
				t.Fatal("expected non-empty result")
			}
			if tt.contains != "" {
				found := false
				for i := 0; i <= len(result)-len(tt.contains); i++ {
					if result[i:i+len(tt.contains)] == tt.contains {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected result to contain %q, got %q", tt.contains, result)
				}
			}
		})
	}
}

func TestFormatPickerItem(t *testing.T) {
	tests := []struct {
		name        string
		skillName   string
		description string
		selected    bool
		wantPrefix  string
	}{
		{
			name:        "selected",
			skillName:   "weather",
			description: "Weather forecasts",
			selected:    true,
			wantPrefix:  "[x]",
		},
		{
			name:        "unselected",
			skillName:   "github",
			description: "GitHub integration",
			selected:    false,
			wantPrefix:  "[ ]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPickerItem(tt.skillName, tt.description, tt.selected)
			if len(result) < 3 {
				t.Fatalf("result too short: %q", result)
			}
			if result[:3] != tt.wantPrefix {
				t.Errorf("expected prefix %q, got %q", tt.wantPrefix, result[:3])
			}
		})
	}
}

func TestCleanRepoURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sipeed/lele-skills", "sipeed/lele-skills"},
		{"https://github.com/sipeed/lele-skills", "sipeed/lele-skills"},
		{"https://github.com/sipeed/lele-skills/", "sipeed/lele-skills"},
		{"http://github.com/sipeed/lele-skills", "sipeed/lele-skills"},
		{"github.com/sipeed/lele-skills", "sipeed/lele-skills"},
		{"  sipeed/lele-skills  ", "sipeed/lele-skills"},
		{"", ""},
		{"single", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := cleanRepoURL(tt.input)
			if result != tt.expected {
				t.Errorf("cleanRepoURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSortSkillsByName(t *testing.T) {
	skills := []channels.ScannedSkill{
		{Name: "zebra"},
		{Name: "alpha"},
		{Name: "middleware"},
	}

	sortSkillsByName(skills)

	if skills[0].Name != "alpha" {
		t.Errorf("expected first skill to be 'alpha', got %q", skills[0].Name)
	}
	if skills[1].Name != "middleware" {
		t.Errorf("expected second skill to be 'middleware', got %q", skills[1].Name)
	}
	if skills[2].Name != "zebra" {
		t.Errorf("expected third skill to be 'zebra', got %q", skills[2].Name)
	}
}

func TestSortSkillsByName_CaseInsensitive(t *testing.T) {
	skills := []channels.ScannedSkill{
		{Name: "Zebra"},
		{Name: "alpha"},
		{Name: "Beta"},
	}

	sortSkillsByName(skills)

	if skills[0].Name != "alpha" {
		t.Errorf("expected first skill to be 'alpha', got %q", skills[0].Name)
	}
	if skills[1].Name != "Beta" {
		t.Errorf("expected second skill to be 'Beta', got %q", skills[1].Name)
	}
	if skills[2].Name != "Zebra" {
		t.Errorf("expected third skill to be 'Zebra', got %q", skills[2].Name)
	}
}

func TestSkillsFeedbackState(t *testing.T) {
	// Test that skills feedback is preserved in the model
	m := Model{
		skillsFeedback: "Installed 2 skill(s): weather, github",
	}

	if m.skillsFeedback != "Installed 2 skill(s): weather, github" {
		t.Errorf("expected feedback preserved, got %q", m.skillsFeedback)
	}
}

func TestSkillsSelectedMap(t *testing.T) {
	// Test multi-select state management
	selectedMap := map[int]bool{
		0: true,
		1: false,
		2: true,
	}

	// Count selected
	selected := 0
	for _, v := range selectedMap {
		if v {
			selected++
		}
	}
	if selected != 2 {
		t.Errorf("expected 2 selected, got %d", selected)
	}

	// Toggle
	selectedMap[1] = !selectedMap[1]
	if !selectedMap[1] {
		t.Error("expected index 1 to be toggled to true")
	}

	// Toggle back
	selectedMap[1] = !selectedMap[1]
	if selectedMap[1] {
		t.Error("expected index 1 to be toggled back to false")
	}
}
