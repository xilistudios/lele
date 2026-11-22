package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestRegistryFolderResolverSurvivesReload(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace

	r := NewAgentRegistry(cfg)

	folder := t.TempDir()
	resolved := func(string) string { return folder }

	r.SetFolderResolver(resolved)

	agent := r.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	if got := agent.ContextBuilder.resolveFolder("any-session"); got != folder {
		t.Fatalf("existing builder not wired: resolveFolder = %q, want %q", got, folder)
	}
	if prompt := agent.ContextBuilder.BuildSystemPromptForSessionWithFolder("native:c:1", "native"); !strings.Contains(prompt, "## Selected Folder") {
		t.Error("system prompt should carry the folder section after SetFolderResolver")
	}

	r.ReloadAgents(cfg)
	agent = r.GetDefaultAgent()
	if got := agent.ContextBuilder.resolveFolder("any-session"); got != folder {
		t.Fatalf("resolver lost after unchanged reload: %q", got)
	}

	updated := config.DefaultConfig()
	updated.Agents.Defaults.Workspace = workspace
	updated.Agents.Defaults.Model = "someother/model"
	r.ReloadAgents(updated)
	agent = r.GetDefaultAgent()
	if got := agent.ContextBuilder.resolveFolder("any-session"); got != folder {
		t.Fatalf("resolver lost after instance recreation: %q", got)
	}

	r.SetFolderResolver(nil)
	if got := agent.ContextBuilder.resolveFolder("any-session"); got != "" {
		t.Fatalf("resolver should be cleared, got %q", got)
	}
}
