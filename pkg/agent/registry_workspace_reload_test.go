package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

func workspaceReloadCfg(t *testing.T, workspace string) *config.Config {
	t.Helper()
	cfg := testCfg(t, []config.AgentConfig{{
		ID:        "alpha",
		Default:   true,
		Workspace: workspace,
	}})
	cfg.Agents.Defaults.Workspace = workspace
	return cfg
}

func TestReloadAgents_WorkspaceChangeUpdatesContext(t *testing.T) {
	pinFileUsageMode(t)
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()

	registry := NewAgentRegistry(workspaceReloadCfg(t, workspaceA))
	first, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("expected the agent to be registered")
	}
	if !strings.Contains(first.ContextBuilder.GetInitialContext(), workspaceA) {
		t.Fatalf("precondition: the prompt must render the initial workspace %q", workspaceA)
	}

	registry.ReloadAgents(workspaceReloadCfg(t, workspaceB))

	second, ok := registry.GetAgent("alpha")
	if !ok {
		t.Fatal("agent alpha missing after reload")
	}
	if second.Workspace != workspaceB {
		t.Fatalf("precondition: the reloaded instance workspace = %q, want %q", second.Workspace, workspaceB)
	}
	if second.ContextBuilder != first.ContextBuilder {
		t.Fatal("precondition: the context builder is expected to survive the reload")
	}

	prompt := second.ContextBuilder.GetInitialContext()
	if !strings.Contains(prompt, workspaceB) {
		t.Errorf("prompt after reload does not render the new workspace %q", workspaceB)
	}
	if strings.Contains(prompt, workspaceA) {
		t.Errorf("prompt after reload still renders the old workspace %q", workspaceA)
	}
}

func TestReloadAgents_WorkspaceChangeReachesSkillTools(t *testing.T) {
	pinFileUsageMode(t)
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeTestSkill(t, workspaceA, "old-skill", "the old workspace skill")
	writeTestSkill(t, workspaceB, "new-skill", "the new workspace skill")

	registry := NewAgentRegistry(workspaceReloadCfg(t, workspaceA))
	first, _ := registry.GetAgent("alpha")
	if res := loadSkillFrom(t, first, "old-skill"); res.IsError {
		t.Fatalf("precondition: load_skill must resolve the initial workspace skill: %s", res.ForLLM)
	}
	if res := loadSkillFrom(t, first, "new-skill"); !res.IsError {
		t.Fatalf("precondition: load_skill must not resolve a skill outside the initial workspace: %s", res.ForLLM)
	}

	registry.ReloadAgents(workspaceReloadCfg(t, workspaceB))

	second, _ := registry.GetAgent("alpha")
	if res := loadSkillFrom(t, second, "new-skill"); res.IsError {
		t.Errorf("load_skill after reload does not see the new workspace skill: %s", res.ForLLM)
	}
	if res := loadSkillFrom(t, second, "old-skill"); !res.IsError {
		t.Errorf("load_skill after reload still sees the old workspace skill: %s", res.ForLLM)
	}
}

func loadSkillFrom(t *testing.T, instance *AgentInstance, name string) *tools.ToolResult {
	t.Helper()
	loadTool, ok := instance.Tools.Get(tools.LoadSkillName)
	if !ok {
		t.Fatal("load_skill tool not registered on the agent")
	}
	ctx := tools.WithAgentToolContext(context.Background(), instance.ID, "s1")
	return loadTool.Execute(ctx, map[string]interface{}{"skill": name})
}
