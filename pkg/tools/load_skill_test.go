package tools

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func TestLoadSkillTool_LoadsContentAndRecordsUsage(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var pinned []string
	tool := NewLoadSkillTool(loader, usage, "coder", nil, func(names []string) { pinned = append(pinned, names...) })

	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather"})
	if res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Instructions for weather.") {
		t.Fatalf("result must carry the full SKILL.md body: %s", res.ForLLM)
	}
	if got := usage.CountsFor("coder")["weather"]; got != 1 {
		t.Fatalf("usage count = %d, want the load recorded", got)
	}
	if !slices.Contains(pinned, "weather") {
		t.Fatalf("onLoaded got %v, want the loaded skill pinned into the prompt", pinned)
	}
}

func TestLoadSkillTool_LoadsSeveralAndDeduplicates(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var pinned []string
	tool := NewLoadSkillTool(loader, usage, "coder", nil, func(names []string) { pinned = append(pinned, names...) })

	res := tool.Execute(context.Background(), map[string]interface{}{
		"skill":  "weather",
		"skills": []interface{}{"github", "weather"},
	})
	if res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	for _, want := range []string{"Instructions for weather.", "Instructions for github."} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("result missing %q: %s", want, res.ForLLM)
		}
	}
	if len(pinned) != 2 {
		t.Fatalf("pinned = %v, want each skill once", pinned)
	}
	if got := usage.CountsFor("coder")["weather"]; got != 1 {
		t.Fatalf("duplicate name counted twice: weather = %d", got)
	}
}

func TestLoadSkillTool_MissingSkillIsAnError(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "nonexistent"})
	if !res.IsError {
		t.Fatalf("unknown skill must be rejected, got: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_PartialMissingStillLoadsTheRest(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	tool := NewLoadSkillTool(loader, nil, "coder", nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"skills": []interface{}{"weather", "ghost"}})
	if res.IsError {
		t.Fatalf("partial load must succeed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Instructions for weather.") || !strings.Contains(res.ForLLM, "ghost") {
		t.Fatalf("result must load weather and report ghost as missing: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_RejectsFilteredDisabledAndTraversal(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github")
	if err := loader.GetConfigManager().SetDisabled("github"); err != nil {
		t.Fatal(err)
	}
	tool := NewLoadSkillTool(loader, nil, "coder", []string{"weather"}, nil)

	for _, name := range []string{"github", "../weather", "weather/../github"} {
		res := tool.Execute(context.Background(), map[string]interface{}{"skill": name})
		if !res.IsError {
			t.Errorf("skill %q must be rejected (filtered, disabled or traversal), got: %s", name, res.ForLLM)
		}
	}
}

func TestLoadSkillTool_NoNamesIsAnError(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("missing skill argument must be rejected, got: %s", res.ForLLM)
	}
}
