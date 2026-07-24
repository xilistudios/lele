package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGroupsConfig_ParseJSON(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.lele/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		},
		"groups": {
			"list": [
				{
					"id": "review-panel",
					"participants": ["architect", "coder", "security-auditor"],
					"strategy": "moa",
					"rounds": 2,
					"moderator": "architect",
					"max_turns": 12,
					"max_tokens_per_turn": 4096,
					"total_token_budget": 60000,
					"stop_keywords": ["CONSENSUS", "FINAL"],
					"parallel": true
				}
			]
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Groups.List) != 1 {
		t.Fatalf("groups.list len = %d, want 1", len(cfg.Groups.List))
	}

	g := cfg.Groups.List[0]
	if g.ID != "review-panel" {
		t.Errorf("ID = %q, want %q", g.ID, "review-panel")
	}
	if len(g.Participants) != 3 {
		t.Errorf("Participants len = %d, want 3", len(g.Participants))
	}
	if g.Participants[0] != "architect" || g.Participants[1] != "coder" || g.Participants[2] != "security-auditor" {
		t.Errorf("Participants = %v", g.Participants)
	}
	if g.Strategy != "moa" {
		t.Errorf("Strategy = %q, want %q", g.Strategy, "moa")
	}
	if g.Rounds != 2 {
		t.Errorf("Rounds = %d, want 2", g.Rounds)
	}
	if g.Moderator != "architect" {
		t.Errorf("Moderator = %q, want %q", g.Moderator, "architect")
	}
	if g.MaxTurns != 12 {
		t.Errorf("MaxTurns = %d, want 12", g.MaxTurns)
	}
	if g.MaxTokensPerTurn != 4096 {
		t.Errorf("MaxTokensPerTurn = %d, want 4096", g.MaxTokensPerTurn)
	}
	if g.TotalTokenBudget != 60000 {
		t.Errorf("TotalTokenBudget = %d, want 60000", g.TotalTokenBudget)
	}
	if len(g.StopKeywords) != 2 || g.StopKeywords[0] != "CONSENSUS" || g.StopKeywords[1] != "FINAL" {
		t.Errorf("StopKeywords = %v", g.StopKeywords)
	}
	if !g.Parallel {
		t.Errorf("Parallel = false, want true")
	}
}

func TestGroupsConfig_ParseJSON_Empty(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.lele/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Groups.List) != 0 {
		t.Errorf("groups.list should be empty, got %d", len(cfg.Groups.List))
	}
}

func TestGroupProfile_Validate_Valid(t *testing.T) {
	gp := GroupProfile{
		ID:           "review-panel",
		Participants: []string{"architect", "coder"},
		Strategy:     StrategyMoA,
		Moderator:    "architect",
	}
	if err := gp.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestGroupProfile_Validate_AllStrategies(t *testing.T) {
	strategies := []string{StrategyRoundRobin, StrategyMoA, StrategyModerator, StrategyPipeline}
	for _, s := range strategies {
		gp := GroupProfile{
			ID:           "test-" + s,
			Participants: []string{"a", "b"},
			Strategy:     s,
		}
		if err := gp.Validate(); err != nil {
			t.Errorf("strategy %q: expected no error, got: %v", s, err)
		}
	}
}

func TestGroupProfile_Validate_EmptyID(t *testing.T) {
	gp := GroupProfile{
		Participants: []string{"a"},
		Strategy:     StrategyMoA,
	}
	err := gp.Validate()
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
	if err.Error() != "groups: group ID is required" {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGroupProfile_Validate_EmptyParticipants(t *testing.T) {
	gp := GroupProfile{
		ID:       "test",
		Strategy: StrategyMoA,
	}
	err := gp.Validate()
	if err == nil {
		t.Fatal("expected error for empty participants")
	}
	if err.Error() != `groups["test"]: participants list is empty` {
		t.Errorf("error = %q", err.Error())
	}
}

func TestGroupProfile_Validate_InvalidStrategy(t *testing.T) {
	gp := GroupProfile{
		ID:           "test",
		Participants: []string{"a"},
		Strategy:     "invalid_strategy",
	}
	err := gp.Validate()
	if err == nil {
		t.Fatal("expected error for invalid strategy")
	}
	expected := `groups["test"]: invalid strategy "invalid_strategy"`
	if len(err.Error()) < len(expected) || err.Error()[:len(expected)] != expected {
		t.Errorf("error = %q, want prefix %q", err.Error(), expected)
	}
}

func TestGroupProfile_Validate_ModeratorNotParticipant(t *testing.T) {
	gp := GroupProfile{
		ID:           "test",
		Participants: []string{"architect", "coder"},
		Strategy:     StrategyMoA,
		Moderator:    "ghost",
	}
	err := gp.Validate()
	if err == nil {
		t.Fatal("expected error for moderator not in participants")
	}
	expected := `groups["test"]: moderator "ghost" is not in participants list`
	if err.Error() != expected {
		t.Errorf("error = %q, want %q", err.Error(), expected)
	}
}

func TestGroupProfile_Validate_ModeratorInParticipants(t *testing.T) {
	gp := GroupProfile{
		ID:           "test",
		Participants: []string{"architect", "coder"},
		Strategy:     StrategyModerator,
		Moderator:    "coder",
	}
	if err := gp.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestGroupProfile_Validate_EmptyModeratorOK(t *testing.T) {
	gp := GroupProfile{
		ID:           "test",
		Participants: []string{"a", "b"},
		Strategy:     StrategyRoundRobin,
	}
	if err := gp.Validate(); err != nil {
		t.Errorf("expected no error for empty moderator, got: %v", err)
	}
}

func TestGroupsConfig_ValidateGroups_MultipleGroups(t *testing.T) {
	gc := GroupsConfig{
		List: []GroupProfile{
			{ID: "g1", Participants: []string{"a"}, Strategy: StrategyMoA},
			{ID: "g2", Participants: []string{"b"}, Strategy: StrategyRoundRobin},
		},
	}
	if err := gc.ValidateGroups(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestGroupsConfig_ValidateGroups_SecondGroupInvalid(t *testing.T) {
	gc := GroupsConfig{
		List: []GroupProfile{
			{ID: "g1", Participants: []string{"a"}, Strategy: StrategyMoA},
			{ID: "g2", Participants: []string{"b"}, Strategy: "bad"},
		},
	}
	err := gc.ValidateGroups()
	if err == nil {
		t.Fatal("expected error for second group")
	}
	if !strings.Contains(err.Error(), `groups["g2"]`) {
		t.Errorf("error should mention group g2, got: %v", err)
	}
}

func TestGroupsConfig_ValidateGroups_Empty(t *testing.T) {
	gc := GroupsConfig{}
	if err := gc.ValidateGroups(); err != nil {
		t.Errorf("expected no error for empty groups, got: %v", err)
	}
}

func TestValidateEditableDocument_WithGroups(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "~/.lele/workspace",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Groups: GroupsConfig{
			List: []GroupProfile{
				{ID: "panel", Participants: []string{"a", "b"}, Strategy: StrategyMoA},
			},
		},
	}
	errors := ValidateEditableDocument(doc)
	for _, e := range errors {
		if e.Path == "groups" {
			t.Errorf("unexpected groups validation error: %s", e.Message)
		}
	}
}

func TestValidateEditableDocument_InvalidGroup(t *testing.T) {
	doc := &EditableDocument{
		Agents: EditableAgentsConfig{
			Defaults: EditableAgentDefaults{
				Workspace:         "~/.lele/workspace",
				Provider:          "openai",
				Model:             "gpt-4",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
		Groups: GroupsConfig{
			List: []GroupProfile{
				{ID: "panel", Participants: []string{"a"}, Strategy: "invalid"},
			},
		},
	}
	validationErrors := ValidateEditableDocument(doc)
	found := false
	for _, e := range validationErrors {
		if e.Path == "groups" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected groups validation error, got none")
	}
}
