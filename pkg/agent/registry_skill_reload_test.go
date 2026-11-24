package agent

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
)

func newSkillReloadFixture(t *testing.T, name string, allowedTools []string) (*config.Config, string) {
	t.Helper()
	pinFileUsageMode(t)
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
func TestLoadSkill_MixedCaseNamePinsCanonically(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent("skill-agent")
	if !ok {
		t.Fatal("expected the agent to be registered")
	}

	res := loadSkillInto(t, agent, "s1", "ZETA")
	if res.IsError {
		t.Fatalf("mixed-case load failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "### Skill: zeta") {
		t.Fatalf("result must report the canonical skill name: %s", res.ForLLM)
	}
	if got := agent.ContextBuilder.PinnedSkills("s1"); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("pinned = %v, want the canonical name", got)
	}

	prompt := agent.ContextBuilder.BuildSystemPromptForSession("s1", "")
	if !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the canonical pin never reached the prompt: %s", prompt)
	}
	if strings.Contains(prompt, "### Skill: ZETA") {
		t.Fatalf("the prompt kept the caller casing: %s", prompt)
	}
}

func TestSubagentUnloadKeepsOwnerSessionPins(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent("skill-agent")
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if res := loadSkillInto(t, agent, "s1", "zeta"); res.IsError {
		t.Fatalf("precondition: the owner session must pin zeta: %s", res.ForLLM)
	}

	loadTool, ok := agent.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill tool not registered on the agent")
	}
	subagentCtx := tools.WithSubagentScope(tools.WithAgentToolContext(context.Background(), agent.ID, "s1"))
	res := loadTool.Execute(subagentCtx, map[string]interface{}{"skill": "zeta", "unload": true})
	if res.IsError {
		t.Fatalf("a subagent unload must not fail: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "no session pins to unload") {
		t.Fatalf("result must explain that a subagent run has no pins: %s", res.ForLLM)
	}

	if got := agent.ContextBuilder.PinnedSkills("s1"); !slices.Equal(got, []string{"zeta"}) {
		t.Fatalf("owner session pins = %v, want zeta still pinned", got)
	}
	if prompt := agent.ContextBuilder.BuildSystemPromptForSession("s1", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the owner session lost the pinned skill: %s", prompt)
	}
}

func newSpawnSkillReloadFixture(t *testing.T, name string) (*config.Config, string) {
	t.Helper()
	pinFileUsageMode(t)
	workspace := t.TempDir()
	for _, skillName := range []string{"alpha", "beta", "zeta"} {
		writeTestSkill(t, workspace, skillName, "the "+skillName+" skill")
	}
	cfg := testCfg(t, []config.AgentConfig{spawnSkillAgent(name, workspace)})
	cfg.Agents.Defaults.Workspace = workspace
	return cfg, workspace
}

func spawnSkillAgent(name, workspace string) config.AgentConfig {
	return config.AgentConfig{
		ID:        "skill-agent",
		Default:   true,
		Name:      name,
		Workspace: workspace,
		Tools:     []string{"spawn", "load_skill", "list_skills", "search_skill"},
		Subagents: &config.SubagentsConfig{AllowAgents: []string{"skill-agent"}},
	}
}

func TestReload_SpawnAgentSubagentCloneGetsReboundTools(t *testing.T) {
	cfg, workspace := newSpawnSkillReloadFixture(t, "first")
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	sm, ok := al.toolCoordinator.GetSubagents()["skill-agent"]
	if !ok {
		t.Fatal("expected a subagent manager for the spawn agent")
	}
	if _, ok := sm.GetToolRegistry().Get(tools.LoadSkillName); !ok {
		t.Fatal("load_skill missing from the pre-reload subagent clone")
	}
	beforeRegistry := sm.GetToolRegistry()

	first, _ := al.registry.GetAgent("skill-agent")
	reloadedCfg := testCfg(t, []config.AgentConfig{spawnSkillAgent("second", workspace)})
	reloadedCfg.Agents.Defaults.Workspace = workspace
	al.ReloadRegistry(reloadedCfg)

	second, _ := al.registry.GetAgent("skill-agent")
	if second == first {
		t.Fatal("precondition: a name change must recreate the instance")
	}
	if sm.GetToolRegistry() == beforeRegistry {
		t.Fatal("the spawn agent's subagent clone was never refreshed")
	}

	agentTool, ok := second.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill missing from the reloaded agent")
	}
	cloneTool, ok := sm.GetToolRegistry().Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill missing from the refreshed subagent clone")
	}
	if cloneTool != agentTool {
		t.Fatal("the spawn agent's subagent clone still holds the pre-reload skill tool")
	}

	ctx := tools.WithAgentToolContext(context.Background(), second.ID, "s1")
	if res := cloneTool.Execute(ctx, map[string]interface{}{"skill": "zeta"}); res.IsError {
		t.Fatalf("subagent skill tool failed: %s", res.ForLLM)
	}
	if prompt := second.ContextBuilder.BuildSystemPromptForSession("s1", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the clone's load never reached the surviving builder: %s", prompt)
	}
}
func TestReload_UnchangedSpawnAgentReclonesSubagentTools(t *testing.T) {
	cfg, _ := newSpawnSkillReloadFixture(t, "first")
	al := NewAgentLoop(cfg, bus.NewMessageBus())

	sm, ok := al.toolCoordinator.GetSubagents()["skill-agent"]
	if !ok {
		t.Fatal("expected a subagent manager for the spawn agent")
	}
	agent, _ := al.registry.GetAgent("skill-agent")
	if _, ok := agent.Tools.Get("spawn"); !ok {
		t.Fatal("precondition: the spawn agent must carry spawn for this reload path")
	}
	agentTool, ok := agent.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill missing from the spawn agent")
	}

	sm.SetTools(tools.NewToolRegistry())

	al.ReloadRegistry(cfg)

	survived, _ := al.registry.GetAgent("skill-agent")
	if survived != agent {
		t.Fatal("precondition: an unchanged agent config must keep the same instance")
	}
	cloneTool, ok := sm.GetToolRegistry().Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("the reload did not re-clone the spawn agent's subagent tools")
	}
	if cloneTool != agentTool {
		t.Fatal("the subagent clone does not hold the surviving agent's skill tool")
	}

	ctx := tools.WithAgentToolContext(context.Background(), agent.ID, "s1")
	if res := cloneTool.Execute(ctx, map[string]interface{}{"skill": "zeta"}); res.IsError {
		t.Fatalf("subagent skill tool failed: %s", res.ForLLM)
	}
	if prompt := agent.ContextBuilder.BuildSystemPromptForSession("s1", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the clone's load never reached the surviving builder: %s", prompt)
	}
}

func reloadCapabilitiesCfg(t *testing.T, name, workspace string, vision, video bool) *config.Config {
	t.Helper()
	cfg := testCfg(t, []config.AgentConfig{{
		ID:        "skill-agent",
		Default:   true,
		Name:      name,
		Workspace: workspace,
	}})
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"testprovider": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "test-key",
				APIBase: "https://test.example.com/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"test-model": {Vision: vision, Video: video},
			},
		},
	}
	return cfg
}

func TestReload_ReappliesModelCapabilities(t *testing.T) {
	cfg, _ := newSkillReloadFixture(t, "first", nil)
	registry := NewAgentRegistry(cfg)
	first, ok := registry.GetAgent("skill-agent")
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if prompt := first.ContextBuilder.GetInitialContext(); strings.Contains(prompt, "`read_image`") || strings.Contains(prompt, "`read_video`") {
		t.Fatalf("precondition: a model without vision or video must not advertise the media tools: %s", prompt)
	}

	registry.ReloadAgents(reloadCapabilitiesCfg(t, "second", first.Workspace, true, false))
	vision, _ := registry.GetAgent("skill-agent")
	if vision == first {
		t.Fatal("precondition: a name change must recreate the instance")
	}
	if vision.ContextBuilder != first.ContextBuilder {
		t.Fatal("precondition: the context builder is expected to survive the reload")
	}
	if !vision.SupportsImages {
		t.Fatal("precondition: the recreated instance must carry the new vision capability")
	}
	if prompt := vision.ContextBuilder.GetInitialContext(); !strings.Contains(prompt, "`read_image`") {
		t.Fatalf("the surviving builder did not get SetVisionSupported re-applied: %s", prompt)
	}

	registry.ReloadAgents(reloadCapabilitiesCfg(t, "third", first.Workspace, false, true))
	video, _ := registry.GetAgent("skill-agent")
	if video.SupportsImages || !video.SupportsVideo {
		t.Fatalf("precondition: the recreated instance must carry the new video capability: images=%t video=%t", video.SupportsImages, video.SupportsVideo)
	}
	if video.ContextBuilder != first.ContextBuilder {
		t.Fatal("precondition: the context builder is expected to survive the reload")
	}
	prompt := video.ContextBuilder.GetInitialContext()
	if !strings.Contains(prompt, "`read_video`") {
		t.Fatalf("the surviving builder did not get SetVideoSupported re-applied: %s", prompt)
	}
	if strings.Contains(prompt, "`read_image`") {
		t.Fatalf("the surviving builder kept the stale vision capability: %s", prompt)
	}
}
