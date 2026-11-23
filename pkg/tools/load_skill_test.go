package tools

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func pinAll(_ string, names []string) ([]string, []string, []string) {
	return names, nil, nil
}

func TestLoadSkillTool_LoadsContentAndRecordsUsage(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var pinned []string
	pin := func(_ string, names []string) ([]string, []string, []string) {
		pinned = append(pinned, names...)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, nil, nil)

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
		t.Fatalf("pin got %v, want the loaded skill pinned into the prompt", pinned)
	}
}

func TestLoadSkillTool_LoadsSeveralAndDeduplicates(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var pinned []string
	pin := func(_ string, names []string) ([]string, []string, []string) {
		pinned = append(pinned, names...)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, nil, nil)

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
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, nil, nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "nonexistent"})
	if !res.IsError {
		t.Fatalf("unknown skill must be rejected, got: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_PartialMissingStillLoadsTheRest(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pinAll, nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"skills": []interface{}{"weather", "ghost"}})
	if res.IsError {
		t.Fatalf("partial load must succeed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Instructions for weather.") || !strings.Contains(res.ForLLM, "ghost") {
		t.Fatalf("result must load weather and report ghost as missing: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_RejectsFilteredDisabledAndTraversal(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github", "daily")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	if err := loader.GetConfigManager().SetDisabled("github"); err != nil {
		t.Fatal(err)
	}
	var pinned []string
	pin := func(_ string, names []string) ([]string, []string, []string) {
		pinned = append(pinned, names...)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", []string{"weather", "github"}, pin, nil, nil)

	allowed := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather"})
	if allowed.IsError {
		t.Fatalf("an allowed and enabled skill must load: %s", allowed.ForLLM)
	}
	if !strings.Contains(allowed.ForLLM, "Instructions for weather.") {
		t.Fatalf("the positive control lost the body: %s", allowed.ForLLM)
	}
	if !slices.Equal(pinned, []string{"weather"}) {
		t.Fatalf("pin got %v, want only the allowed skill", pinned)
	}

	for _, name := range []string{"daily", "github", "../weather", "weather/../github"} {
		res := tool.Execute(context.Background(), map[string]interface{}{"skill": name})
		if !res.IsError {
			t.Errorf("skill %q must be refused (filtered, disabled or traversal), got: %s", name, res.ForLLM)
			continue
		}
		if !strings.Contains(res.ForLLM, "not found or not available") {
			t.Errorf("skill %q must be reported as unavailable, got: %s", name, res.ForLLM)
		}
	}
	if !slices.Equal(pinned, []string{"weather"}) {
		t.Fatalf("a refused load must never pin: %v", pinned)
	}
}

func TestLoadSkillTool_ResolvesNamesCaseInsensitively(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var pinned []string
	pin := func(_ string, names []string) ([]string, []string, []string) {
		pinned = append(pinned, names...)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, nil, nil)

	for _, name := range []string{"Weather", "WEATHER", "wEaThEr"} {
		res := tool.Execute(context.Background(), map[string]interface{}{"skill": name})
		if res.IsError {
			t.Fatalf("load %q failed: %s", name, res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "### Skill: weather") {
			t.Fatalf("load %q must report the canonical skill name: %s", name, res.ForLLM)
		}
	}

	if !slices.Equal(pinned, []string{"weather", "weather", "weather"}) {
		t.Fatalf("pin got %v, want the canonical name pinned", pinned)
	}
	if got := usage.CountsFor("coder")["weather"]; got != 3 {
		t.Fatalf("usage[weather] = %d, want the canonical name counted", got)
	}
	if got := usage.CountsFor("coder")["Weather"]; got != 0 {
		t.Fatalf("usage recorded the caller casing (%d), want the canonical name", got)
	}
}

func TestLoadSkillTool_LoadsMixedCaseBatchOnce(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github")
	var pinned []string
	pin := func(_ string, names []string) ([]string, []string, []string) {
		pinned = append(pinned, names...)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pin, nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{
		"skills": []interface{}{"Weather", "GitHub", "WEATHER"},
	})
	if res.IsError {
		t.Fatalf("mixed-case batch failed: %s", res.ForLLM)
	}
	for _, want := range []string{"### Skill: weather", "### Skill: github"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("result missing %q: %s", want, res.ForLLM)
		}
	}
	if strings.Contains(res.ForLLM, "### Skill: Weather") || strings.Contains(res.ForLLM, "### Skill: GitHub") {
		t.Fatalf("result kept the caller casing: %s", res.ForLLM)
	}
	if !slices.Equal(pinned, []string{"weather", "github"}) {
		t.Fatalf("pin got %v, want each canonical name once", pinned)
	}
}

func TestLoadSkillTool_NoNamesIsAnError(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, nil, nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if !res.IsError {
		t.Fatalf("missing skill argument must be rejected, got: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_PinsUnderContextSessionKey(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	var keys []string
	pin := func(sessionKey string, names []string) ([]string, []string, []string) {
		keys = append(keys, sessionKey)
		return names, nil, nil
	}
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pin, nil, nil)

	if res := tool.Execute(WithAgentToolContext(context.Background(), "coder", "chat-a"),
		map[string]interface{}{"skill": "weather"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	if res := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	if len(keys) != 2 || keys[0] != "chat-a" || keys[1] != "" {
		t.Fatalf("pin saw session keys %v, want [chat-a \"\"]", keys)
	}
}

func TestLoadSkillTool_AlreadyPinnedNoDoubleCount(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	pin := func(string, []string) ([]string, []string, []string) {
		return nil, []string{"weather"}, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, nil, nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather"})
	if res.IsError {
		t.Fatalf("already-pinned load must not fail: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Already in context") {
		t.Fatalf("result must say the skill is already in context: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "Instructions for weather.") {
		t.Fatalf("already-pinned load must not re-send the body: %s", res.ForLLM)
	}
	if got := usage.CountsFor("coder")["weather"]; got != 0 {
		t.Fatalf("usage count = %d, want no increment for an already pinned skill", got)
	}
}

func TestLoadSkillTool_BudgetRejectionIsActionable(t *testing.T) {
	loader := newSkillToolLoader(t, "github")
	pin := func(string, []string) ([]string, []string, []string) {
		return nil, nil, []string{"github"}
	}
	pinned := func(sessionKey string) []string {
		if sessionKey != "chat-a" {
			t.Fatalf("pinned saw session key %q, want chat-a", sessionKey)
		}
		return []string{"alpha"}
	}
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pin, nil, pinned)

	ctx := WithAgentToolContext(context.Background(), "coder", "chat-a")
	res := tool.Execute(ctx, map[string]interface{}{"skill": "github"})
	if !res.IsError {
		t.Fatalf("a fully rejected load must be an error, got: %s", res.ForLLM)
	}
	for _, want := range []string{"github", "unload", "32 KiB", "Currently pinned: \"alpha\""} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("result must mention %q: %s", want, res.ForLLM)
		}
	}
}

func TestLoadSkillTool_Unload(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	var unpinned []string
	unpin := func(_ string, names []string) ([]string, []string) {
		unpinned = append(unpinned, names...)
		return []string{"weather"}, []string{"ghost"}
	}
	pinCalled := false
	pin := func(string, []string) ([]string, []string, []string) {
		pinCalled = true
		return nil, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, unpin, nil)

	for _, args := range []map[string]interface{}{
		{"skill": "weather", "unload": true},
		{"skills": []interface{}{"weather", "ghost"}, "unload": true},
	} {
		res := tool.Execute(context.Background(), args)
		if res.IsError {
			t.Fatalf("unload must be idempotent, got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "weather") || !strings.Contains(res.ForLLM, "ghost") {
			t.Fatalf("result must name the removed and the absent skill: %s", res.ForLLM)
		}
	}
	if len(unpinned) != 3 {
		t.Fatalf("unpin saw %v, want each name in both calls", unpinned)
	}
	if pinCalled {
		t.Fatal("unload must never pin")
	}
	if got := usage.CountsFor("coder")["weather"]; got != 0 {
		t.Fatalf("usage count = %d, want unload to leave usage untouched", got)
	}
}

func TestLoadSkillTool_UnloadWithoutNamesIsError(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, pinAll, nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"unload": true})
	if !res.IsError {
		t.Fatalf("unload without a skill name must be rejected, got: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_UnloadCanonicalisesTheName(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	var unpinned []string
	unpin := func(_ string, names []string) ([]string, []string) {
		unpinned = append(unpinned, names...)
		return names, nil
	}
	pinned := func(string) []string { return []string{"weather"} }
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pinAll, unpin, pinned)

	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "Weather", "unload": true})
	if res.IsError {
		t.Fatalf("unload failed: %s", res.ForLLM)
	}
	if !slices.Equal(unpinned, []string{"weather"}) {
		t.Fatalf("unpin got %v, want the canonical pinned name", unpinned)
	}
}

func TestLoadSkillTool_SubagentUnloadLeavesOwnerPins(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	unpinCalled := false
	unpin := func(string, []string) ([]string, []string) {
		unpinCalled = true
		return []string{"weather"}, nil
	}
	tool := NewLoadSkillTool(loader, nil, "coder", nil, pinAll, unpin, nil)

	subagentCtx := WithSubagentScope(WithAgentToolContext(context.Background(), "coder", "chat-a"))
	res := tool.Execute(subagentCtx, map[string]interface{}{"skill": "weather", "unload": true})
	if res.IsError {
		t.Fatalf("a subagent unload must not fail: %s", res.ForLLM)
	}
	if unpinCalled {
		t.Fatal("a subagent unload must never reach the owner session's pins")
	}
	if !strings.Contains(res.ForLLM, "no session pins to unload") {
		t.Fatalf("result must explain that a subagent run has no pins: %s", res.ForLLM)
	}

	ownerCtx := WithAgentToolContext(context.Background(), "coder", "chat-a")
	if res := tool.Execute(ownerCtx, map[string]interface{}{"skill": "weather", "unload": true}); res.IsError {
		t.Fatalf("the owner session must still be able to unload: %s", res.ForLLM)
	}
	if !unpinCalled {
		t.Fatal("the owner session's unload never reached the pin owner")
	}
}

func TestLoadSkillTool_NilPinOwnerIsLoud(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, nil, nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather"})
	if !res.IsError {
		t.Fatalf("a missing pin owner must fail loudly, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not configured") {
		t.Fatalf("error must name the missing configuration: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_NilUnpinOwnerIsLoud(t *testing.T) {
	tool := NewLoadSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil, pinAll, nil, nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"skill": "weather", "unload": true})
	if !res.IsError {
		t.Fatalf("a missing unpin owner must fail loudly, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not configured") {
		t.Fatalf("error must name the missing configuration: %s", res.ForLLM)
	}
}

func TestLoadSkillTool_SubagentScopeDoesNotPin(t *testing.T) {
	loader := newSkillToolLoader(t, "weather")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	pinCalled := false
	pin := func(string, []string) ([]string, []string, []string) {
		pinCalled = true
		return nil, nil, nil
	}
	tool := NewLoadSkillTool(loader, usage, "coder", nil, pin, nil, nil)

	ctx := WithSubagentScope(WithAgentToolContext(context.Background(), "coder", "chat-a"))
	res := tool.Execute(ctx, map[string]interface{}{"skill": "weather"})
	if res.IsError {
		t.Fatalf("subagent load failed: %s", res.ForLLM)
	}
	if pinCalled {
		t.Fatal("a subagent load must never pin into the owner session")
	}
	if !strings.Contains(res.ForLLM, "Instructions for weather.") {
		t.Fatalf("subagent must still receive the body: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "subagent run only") {
		t.Fatalf("result must say the body is not pinned: %s", res.ForLLM)
	}
	if got := usage.CountsFor("coder")["weather"]; got != 1 {
		t.Fatalf("usage count = %d, want the subagent load recorded", got)
	}
}
