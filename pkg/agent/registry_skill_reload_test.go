package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
)

func newSkillReloadFixture(t *testing.T, name string, allowedTools []string) (*config.Config, string) {
	t.Helper()
	workspace := t.TempDir()
	for _, skillName := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, skillName, "the "+skillName+" skill")
	}
	cfg := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      name,
		Workspace: workspace,
		Tools:     allowedTools,
	}})
	cfg.Agents.Defaults.Workspace = workspace
	return cfg, config.GetLeleDir()
}

func loadSkillInto(t *testing.T, agent *AgentInstance, sessionKey, skillName string) *tools.ToolResult {
	t.Helper()
	loadTool, ok := agent.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill tool not registered on the agent")
	}
	ctx := tools.WithAgentToolContext(context.Background(), agent.ID, sessionKey)
	return loadTool.Execute(ctx, map[string]interface{}{"skill": skillName})
}

func TestReload_LoadSkillReachesSurvivingBuilder(t *testing.T) {
	cfg, configDir := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)

	first, ok := registry.GetAgent("skill-agent")
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if res := loadSkillInto(t, first, "s1", "zeta"); res.IsError {
		t.Fatalf("pre-reload load failed: %s", res.ForLLM)
	}

	reloadedCfg := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "second",
		Workspace: first.Workspace,
	}})
	reloadedCfg.Agents.Defaults.Workspace = first.Workspace
	registry.ReloadAgents(reloadedCfg)

	second, _ := registry.GetAgent("skill-agent")
	if second == first {
		t.Fatal("precondition: a name change must recreate the instance")
	}
	if second.ContextBuilder != first.ContextBuilder {
		t.Fatal("precondition: the context builder is expected to survive the reload")
	}

	if res := loadSkillInto(t, second, "s1", "eta"); res.IsError {
		t.Fatalf("post-reload load failed: %s", res.ForLLM)
	}

	prompt := second.ContextBuilder.BuildSystemPromptForSession("s1", "")
	if !strings.Contains(prompt, "### Skill: eta") {
		t.Fatalf("the post-reload load never reached the surviving builder: %s", prompt)
	}
	if !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the pre-reload pin was lost: %s", prompt)
	}

	window := skillNamesInPrompt(t, second.ContextBuilder.GetInitialContext())
	if len(window) != 5 || !containsName(window, "eta") {
		t.Fatalf("top-5 window after the reload = %v, want eta ranked in it", window)
	}

	persisted := skills.NewUsageStore(filepath.Join(configDir, "skills_usage.json"))
	counts := persisted.CountsFor(second.ID)
	if counts["eta"] != 1 || counts["zeta"] != 1 {
		t.Fatalf("persisted usage = %v, want both loads recorded under %q", counts, second.ID)
	}
}

func TestReload_ListSkillsExcludesReloadedPin(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("skill-agent")

	if res := loadSkillInto(t, first, "s1", "zeta"); res.IsError {
		t.Fatalf("pre-reload load failed: %s", res.ForLLM)
	}

	reloadedCfg := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "second",
		Workspace: first.Workspace,
	}})
	reloadedCfg.Agents.Defaults.Workspace = first.Workspace
	registry.ReloadAgents(reloadedCfg)
	second, _ := registry.GetAgent("skill-agent")

	listTool, ok := second.Tools.Get(tools.ListSkillsName)
	if !ok {
		t.Fatal("list_skills tool not registered on the agent")
	}
	res := listTool.Execute(tools.WithAgentToolContext(context.Background(), second.ID, "s1"), map[string]interface{}{})
	if res.IsError {
		t.Fatalf("list_skills failed: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "- zeta ") {
		t.Fatalf("list_skills still offers the reloaded agent's pinned skill: %s", res.ForLLM)
	}
}

func TestReload_AllowlistReappliedToSkillTools(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("skill-agent")

	restricted := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "first",
		Workspace: first.Workspace,
		Tools:     []string{"read_file"},
	}})
	restricted.Agents.Defaults.Workspace = first.Workspace
	registry.ReloadAgents(restricted)

	second, _ := registry.GetAgent("skill-agent")
	for _, name := range []string{tools.LoadSkillName, tools.ListSkillsName, tools.SearchSkillName} {
		if _, ok := second.Tools.Get(name); ok {
			t.Fatalf("%s must stay removed by the re-applied allowlist", name)
		}
	}
	prompt := second.ContextBuilder.BuildSystemPrompt()
	if strings.Contains(prompt, "`load_skill`") || strings.Contains(prompt, "`list_skills`") || strings.Contains(prompt, "`search_skill`") {
		t.Fatalf("the prompt still advertises skill tools the allowlist removed: %s", prompt)
	}

	unrestricted := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "first",
		Workspace: first.Workspace,
	}})
	unrestricted.Agents.Defaults.Workspace = first.Workspace
	registry.ReloadAgents(unrestricted)

	third, _ := registry.GetAgent("skill-agent")
	for _, name := range []string{tools.LoadSkillName, tools.ListSkillsName, tools.SearchSkillName} {
		if _, ok := third.Tools.Get(name); !ok {
			t.Fatalf("%s must be registered again once the allowlist allows it", name)
		}
	}
	if res := loadSkillInto(t, third, "s1", "zeta"); res.IsError {
		t.Fatalf("load failed after the allowlist was lifted: %s", res.ForLLM)
	}
	if prompt := third.ContextBuilder.BuildSystemPromptForSession("s1", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("a load after lifting the allowlist never reached the prompt: %s", prompt)
	}
}

func TestReload_SubagentCloneGetsReboundTools(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	subagents := al.toolCoordinator.GetSubagents()
	sm, ok := subagents["skill-agent"]
	if !ok {
		t.Fatalf("expected a subagent manager for skill-agent, got %v", subagents)
	}
	before := sm.GetToolRegistry()
	if before == nil {
		t.Fatal("expected the subagent manager to hold a tool registry")
	}

	first, _ := al.registry.GetAgent("skill-agent")
	reloadedCfg := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "second",
		Workspace: first.Workspace,
	}})
	reloadedCfg.Agents.Defaults.Workspace = first.Workspace
	al.ReloadRegistry(reloadedCfg)

	second, _ := al.registry.GetAgent("skill-agent")
	agentTool, ok := second.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill missing from the reloaded agent")
	}
	subagentTool, ok := sm.GetToolRegistry().Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill missing from the refreshed subagent clone")
	}
	if subagentTool != agentTool {
		t.Fatal("the subagent clone still holds the pre-reload skill tool")
	}

	ctx := tools.WithAgentToolContext(context.Background(), second.ID, "s1")
	if res := subagentTool.Execute(ctx, map[string]interface{}{"skill": "zeta"}); res.IsError {
		t.Fatalf("subagent skill tool failed: %s", res.ForLLM)
	}
	if prompt := second.ContextBuilder.BuildSystemPromptForSession("s1", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the subagent clone's load never reached the surviving builder: %s", prompt)
	}
}

func TestReload_PromptToolsSectionFollowsAllowlist(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("skill-agent")

	if prompt := first.ContextBuilder.BuildSystemPrompt(); !strings.Contains(prompt, "- `exec` -") {
		t.Fatalf("precondition: exec must be advertised before the allowlist change: %s", prompt)
	}

	restricted := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      "first",
		Workspace: first.Workspace,
		Tools:     []string{"read_file"},
	}})
	restricted.Agents.Defaults.Workspace = first.Workspace
	registry.ReloadAgents(restricted)

	second, _ := registry.GetAgent("skill-agent")
	prompt := second.ContextBuilder.BuildSystemPrompt()
	if strings.Contains(prompt, "- `exec` -") {
		t.Fatalf("the prompt still advertises a tool the allowlist removed: %s", prompt)
	}
	if !strings.Contains(prompt, "- `read_file` -") {
		t.Fatalf("the prompt lost the allowlisted tool: %s", prompt)
	}
}
