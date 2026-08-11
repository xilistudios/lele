package skills

import (
	"testing"
)

func TestExtractRepoName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sipeed/lele-skills", "lele-skills"},
		{"user/repo-name", "repo-name"},
		{"owner/name/sub", "sub"},
		{"singlename", "singlename"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractRepoName(tt.input)
			if result != tt.expected {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestExtractDescriptionFromSKILL(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			"with yaml frontmatter",
			`---
name: weather
description: Get current weather and forecasts
---

# Weather Skill

Some content here.`,
			"Get current weather and forecasts",
		},
		{
			"with yaml frontmatter quoted",
			`---
name: weather
description: "Get current weather and forecasts"
---

# Weather Skill`,
			"Get current weather and forecasts",
		},
		{
			"no frontmatter",
			`# Weather Skill

Get current weather and forecasts for any location.`,
			"Get current weather and forecasts for any location.",
		},
		{
			"frontmatter without description",
			`---
name: weather
---

# Weather Skill

This is the description.`,
			"This is the description.",
		},
		{
			"empty content",
			"",
			"",
		},
		{
			"only headers",
			`# Weather Skill

## Description

Some content`,
			"Some content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractDescriptionFromSKILL(tt.content)
			if result != tt.expected {
				t.Errorf("extractDescriptionFromSKILL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestScannedSkill_JSON(t *testing.T) {
	skill := ScannedSkill{
		Name:        "weather",
		Description: "Get weather forecasts",
		Path:        "skills/weather",
		HasSKILL:    true,
	}

	if skill.Name != "weather" {
		t.Errorf("expected name 'weather', got %q", skill.Name)
	}
	if skill.Path != "skills/weather" {
		t.Errorf("expected path 'skills/weather', got %q", skill.Path)
	}
	if !skill.HasSKILL {
		t.Error("expected HasSKILL to be true")
	}
}

func TestScanSkillsResponse(t *testing.T) {
	resp := ScanSkillsResponse{
		Skills: []ScannedSkill{
			{Name: "weather", Path: "weather", HasSKILL: true},
			{Name: "github", Path: "github", HasSKILL: true},
		},
		Repo: "sipeed/lele-skills",
	}

	if len(resp.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(resp.Skills))
	}
	if resp.Repo != "sipeed/lele-skills" {
		t.Errorf("expected repo 'sipeed/lele-skills', got %q", resp.Repo)
	}
}
