package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestContextSkills_ShowsOnlyTopFiveByUsage(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	usage.Inc("coder", "zeta")
	usage.Inc("coder", "zeta")
	usage.Inc("coder", "gamma")

	cb := NewContextBuilder(workspace)
	cb.SetSkillUsage("coder", usage)

	prompt := cb.GetInitialContext()
	names := skillNamesInPrompt(t, prompt)
	if len(names) != 5 {
		t.Fatalf("prompt lists %d skills (%v), want only the top 5", len(names), names)
	}
	if names[0] != "zeta" || names[1] != "gamma" {
		t.Fatalf("prompt skills = %v, want the most used first", names)
	}
	if !strings.Contains(prompt, "search_skill") || !strings.Contains(prompt, "load_skill") {
		t.Fatal("prompt must tell the agent how to discover and load the remaining skills")
	}
}

func TestContextSkills_NoUsageStillListsFive(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	cb := NewContextBuilder(workspace)
	if names := skillNamesInPrompt(t, cb.GetInitialContext()); len(names) != 5 {
		t.Fatalf("prompt lists %d skills (%v), want a top-5 window even without usage", len(names), names)
	}
}

func TestPinSkills_PinsFullInstructionsIntoSessionPrompt(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	writeTestSkill(t, workspace, "alpha", "the alpha skill")

	cb := NewContextBuilder(workspace)
	cb.PinSkills("", []string{"alpha", "alpha"})

	prompt := cb.BuildSystemPromptForSession("", "")
	if !strings.Contains(prompt, "# Loaded Skills") {
		t.Fatalf("session prompt missing the loaded skills section: %s", prompt)
	}
	if !strings.Contains(prompt, "### Skill: alpha") {
		t.Fatalf("session prompt missing the full instructions of the loaded skill: %s", prompt)
	}
	if got := strings.Count(prompt, "### Skill: alpha"); got != 1 {
		t.Fatalf("loaded skill appears %d times, want duplicates collapsed", got)
	}
	if strings.Contains(cb.GetInitialContext(), "# Loaded Skills") {
		t.Fatal("the global prompt cache must never carry session pins")
	}

	cb.InvalidatePromptCache()
	if !strings.Contains(cb.BuildSystemPromptForSession("", ""), "### Skill: alpha") {
		t.Fatal("loaded skill must survive a prompt cache invalidation")
	}
}

func TestNewAgentInstance_LoadSkillPromotesIntoTopFive(t *testing.T) {
	workspace := t.TempDir()
	configDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", configDir)

	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	cfg := createTestConfig(workspace)
	agent := NewAgentInstance(&config.AgentConfig{ID: "skill-agent", Workspace: workspace}, &cfg.Agents.Defaults, cfg)

	before := skillNamesInPrompt(t, agent.ContextBuilder.GetInitialContext())
	if len(before) != 5 {
		t.Fatalf("initial prompt lists %d skills, want 5", len(before))
	}

	loadTool, ok := agent.Tools.Get("load_skill")
	if !ok {
		t.Fatal("load_skill tool not registered on the agent")
	}
	if _, ok := agent.Tools.Get("search_skill"); !ok {
		t.Fatal("search_skill tool not registered on the agent")
	}

	res := loadTool.Execute(tools.WithAgentToolContext(context.Background(), agent.ID, "s1"), map[string]interface{}{"skill": "zeta"})
	if res.IsError {
		t.Fatalf("load_skill failed: %s", res.ForLLM)
	}

	after := agent.ContextBuilder.GetInitialContext()
	names := skillNamesInPrompt(t, after)
	if len(names) != 5 || names[0] != "zeta" {
		t.Fatalf("prompt skills after load = %v, want zeta promoted into the top 5", names)
	}
	if strings.Contains(after, "### Skill: zeta") {
		t.Fatal("the global prompt cache must not carry the loaded skill body")
	}
	if !strings.Contains(agent.ContextBuilder.BuildSystemPromptForSession("s1", ""), "### Skill: zeta") {
		t.Fatal("loaded skill instructions must be pinned into the session prompt")
	}

	persisted := skills.NewUsageStore(filepath.Join(configDir, "skills_usage.json"))
	if got := persisted.CountsFor(agent.ID)["zeta"]; got != 1 {
		t.Fatalf("persisted usage for agent %q = %d, want the load recorded under its own ID", agent.ID, got)
	}
}

func TestSkillsInContext_TopFivePlusLoadedWithoutDuplicates(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	usage := skills.NewUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	usage.Inc("coder", "zeta")

	cb := NewContextBuilder(workspace)
	cb.SetSkillUsage("coder", usage)
	cb.PinSkills("", []string{"gamma", "zeta"})

	got := cb.SkillsInContext("")
	if len(got) != 6 {
		t.Fatalf("SkillsInContext = %v, want the top 5 plus the loaded skill outside it, without duplicates", got)
	}
	seen := make(map[string]bool)
	for _, name := range got {
		if seen[name] {
			t.Fatalf("SkillsInContext contains duplicate %q: %v", name, got)
		}
		seen[name] = true
	}
	if !seen["zeta"] || !seen["gamma"] {
		t.Fatalf("SkillsInContext = %v, must include the most used and the loaded skills", got)
	}
}

func TestNewAgentInstance_ListSkillsExcludesWhatIsAlreadyInContext(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	cfg := createTestConfig(workspace)
	agent := NewAgentInstance(&config.AgentConfig{ID: "list-agent", Workspace: workspace}, &cfg.Agents.Defaults, cfg)

	listTool, ok := agent.Tools.Get("list_skills")
	if !ok {
		t.Fatal("list_skills tool not registered on the agent")
	}

	before := listTool.Execute(tools.WithAgentToolContext(context.Background(), agent.ID, "s1"), map[string]interface{}{})
	if before.IsError {
		t.Fatalf("list_skills failed: %s", before.ForLLM)
	}
	inPrompt := skillNamesInPrompt(t, agent.ContextBuilder.GetInitialContext())
	for _, name := range inPrompt {
		if strings.Contains(before.ForLLM, "- "+name+" ") {
			t.Fatalf("list_skills offers %q which is already in the prompt: %s", name, before.ForLLM)
		}
	}
	if !strings.Contains(before.ForLLM, "- zeta ") || !strings.Contains(before.ForLLM, "- gamma ") {
		t.Fatalf("list_skills must offer the skills outside the top 5: %s", before.ForLLM)
	}

	loadTool, _ := agent.Tools.Get("load_skill")
	if res := loadTool.Execute(tools.WithAgentToolContext(context.Background(), agent.ID, "s1"), map[string]interface{}{"skill": "zeta"}); res.IsError {
		t.Fatalf("load_skill failed: %s", res.ForLLM)
	}

	after := listTool.Execute(tools.WithAgentToolContext(context.Background(), agent.ID, "s1"), map[string]interface{}{})
	if strings.Contains(after.ForLLM, "- zeta ") {
		t.Fatalf("loaded skill must disappear from list_skills: %s", after.ForLLM)
	}

	if prompt := agent.ContextBuilder.BuildSystemPromptForSession("s2", ""); strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the pin loaded in s1 leaked into s2's prompt: %s", prompt)
	}
}

func TestNewAgentInstance_SkillToolsRespectAllowlist(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	writeTestSkill(t, workspace, "alpha", "the alpha skill")

	cfg := createTestConfig(workspace)
	agentCfg := &config.AgentConfig{
		ID:        "restricted-agent",
		Workspace: workspace,
		Tools:     []string{"read_file"},
	}
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)

	if _, ok := agent.Tools.Get("load_skill"); ok {
		t.Fatal("load_skill must be removed by the tools allowlist")
	}
	if _, ok := agent.Tools.Get("search_skill"); ok {
		t.Fatal("search_skill must be removed by the tools allowlist")
	}
	if _, ok := agent.Tools.Get("list_skills"); ok {
		t.Fatal("list_skills must be removed by the tools allowlist")
	}
	if _, ok := agent.Tools.Get("read_file"); !ok {
		t.Fatal("allowlisted read_file must survive")
	}
}
