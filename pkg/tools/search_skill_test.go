package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/skills"
)

func newSkillToolLoader(t *testing.T, names ...string) *skills.SkillsLoader {
	t.Helper()
	workspace := t.TempDir()
	for _, name := range names {
		dir := filepath.Join(workspace, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nname: %s\ndescription: the %s skill\n---\n\nInstructions for %s.\n", name, name, name)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return skills.NewSkillsLoader(workspace, "", "", "")
}

func TestSearchSkillTool_RanksAndReportsMatches(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github", "cooking")
	tool := NewSearchSkillTool(loader, nil, "coder", nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"query": "weather"})
	if res.IsError {
		t.Fatalf("search failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "weather") {
		t.Fatalf("result missing the matching skill: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "- cooking") {
		t.Fatalf("result includes an unrelated skill: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "load_skill") {
		t.Fatalf("result should point to load_skill for next step: %s", res.ForLLM)
	}
}

func TestSearchSkillTool_RespectsAgentFilter(t *testing.T) {
	loader := newSkillToolLoader(t, "weather", "github")
	tool := NewSearchSkillTool(loader, nil, "coder", []string{"github"})

	res := tool.Execute(context.Background(), map[string]interface{}{"query": "weather"})
	if strings.Contains(res.ForLLM, "- weather") {
		t.Fatalf("filtered-out skill leaked into results: %s", res.ForLLM)
	}
}

func TestSearchSkillTool_LimitCapsResults(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta", "gamma")
	tool := NewSearchSkillTool(loader, nil, "coder", nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"query": "skill", "limit": 2})
	if got := strings.Count(res.ForLLM, "\n- "); got != 2 {
		t.Fatalf("returned %d entries, want the limit of 2: %s", got, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Found 3") {
		t.Fatalf("result should report the full match count: %s", res.ForLLM)
	}
}

func TestSearchSkillTool_EmptyQueryIsAnError(t *testing.T) {
	tool := NewSearchSkillTool(newSkillToolLoader(t, "weather"), nil, "coder", nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"query": "  "})
	if !res.IsError {
		t.Fatalf("blank query must be rejected, got: %s", res.ForLLM)
	}
}

func TestSearchSkillTool_UsageBreaksScoreTies(t *testing.T) {
	loader := newSkillToolLoader(t, "alpha", "beta")
	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	usage.Inc("coder", "beta")
	usage.Inc("coder", "beta")
	tool := NewSearchSkillTool(loader, usage, "coder", nil)

	res := tool.Execute(context.Background(), map[string]interface{}{"query": "skill"})
	alpha := strings.Index(res.ForLLM, "- alpha")
	beta := strings.Index(res.ForLLM, "- beta")
	if alpha < 0 || beta < 0 || beta > alpha {
		t.Fatalf(" equally relevant skills should list the most used first: %s", res.ForLLM)
	}
}
