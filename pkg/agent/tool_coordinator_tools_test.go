package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/tools"
)

func sharedToolsTestConfig(t *testing.T, agents ...config.AgentConfig) *config.Config {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: agents,
		},
	}
	cfg.Tools.Web.DuckDuckGo.Enabled = true
	cfg.Tools.Web.DuckDuckGo.MaxResults = 5
	return cfg
}

func registerSharedForTest(t *testing.T, cfg *config.Config, agentID string) (*AgentInstance, *tools.SubagentManager) {
	t.Helper()

	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent(agentID)
	if !ok {
		t.Fatalf("agent %q not in registry (ids: %v)", agentID, registry.ListAgentIDs())
	}

	msgBus := bus.NewMessageBus()
	subagents := make(map[string]*tools.SubagentManager)
	bgManagers := make(map[string]*tools.BackgroundProcessManager)

	sm := registerSharedToolsForAgent(agent, cfg, msgBus, registry, nil, agentID, subagents, bgManagers, nil, nil)
	if sm == nil {
		t.Fatal("registerSharedToolsForAgent returned a nil SubagentManager")
	}
	if _, ok := subagents[agentID]; !ok {
		t.Fatalf("no subagent manager recorded for %q", agentID)
	}
	if _, ok := bgManagers[agentID]; !ok {
		t.Fatalf("no background-process manager recorded for %q", agentID)
	}
	return agent, sm
}

func mustHave(t *testing.T, r *tools.ToolRegistry, label string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := r.Get(n); !ok {
			t.Errorf("%s: tool %q missing, have %v", label, n, r.List())
		}
	}
}

func mustNotHave(t *testing.T, r *tools.ToolRegistry, label string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := r.Get(n); ok {
			t.Errorf("%s: tool %q must not be available, have %v", label, n, r.List())
		}
	}
}

var sharedToolNames = []string{
	"web_search", "web_fetch", "i2c", "spi", "send_file",
	"exec", "list_background_execs", "get_background_exec_output", "stop_background_exec",
	"spawn", "wait_for_subagent", "list_active_subagents", "cancel_subagent", "sleep",
}

func TestSharedTools_AllowlistRemovesSharedTools(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "readonly",
		Default: true,
		Tools:   []string{"read_file", "list_dir"},
	})

	agent, sm := registerSharedForTest(t, cfg, "readonly")

	mustHave(t, agent.Tools, "parent", "read_file", "list_dir")
	mustNotHave(t, agent.Tools, "parent", sharedToolNames...)
	mustNotHave(t, agent.Tools, "parent", "write_file", "edit_file", "secret", "group_chat")

	if got, want := agent.Tools.Count(), 2; got != want {
		t.Fatalf("parent tool count = %d, want %d (%v)", got, want, agent.Tools.List())
	}

	sub := sm.GetToolRegistry()
	if sub == nil {
		t.Fatal("subagent manager has no tool registry")
	}
	mustHave(t, sub, "subagent", "read_file", "list_dir")
	mustNotHave(t, sub, "subagent", sharedToolNames...)

	if agent.ContextBuilder.tools == nil {
		t.Fatal("ContextBuilder lost its tool registry")
	} else if _, ok := agent.ContextBuilder.tools.Get("exec"); ok {
		t.Error("ContextBuilder registry still exposes exec after the allowlist")
	}
}

func TestSharedTools_EmptyAllowlistKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t,
		config.AgentConfig{ID: "main", Default: true},
		config.AgentConfig{ID: "explicit", Tools: []string{}},
	)

	for _, id := range []string{"main", "explicit"} {
		agent, sm := registerSharedForTest(t, cfg, id)
		mustHave(t, agent.Tools, id, sharedToolNames...)
		mustHave(t, agent.Tools, id, "read_file", "write_file", "exec")
		if sub := sm.GetToolRegistry(); sub == nil {
			t.Fatalf("%s: no subagent tool registry", id)
		} else {
			mustHave(t, sub, id+" subagent", "exec", "spawn", "web_fetch")
		}
	}
}

func TestSharedTools_AllowlistPartialOverlap(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "shell",
		Default: true,
		Tools:   []string{"read_file", "exec"},
	})

	agent, sm := registerSharedForTest(t, cfg, "shell")

	mustHave(t, agent.Tools, "parent", "read_file", "exec")
	mustNotHave(t, agent.Tools, "parent", "spawn", "web_search", "web_fetch", "send_file", "sleep")
	mustNotHave(t, agent.Tools, "parent", "list_background_execs", "get_background_exec_output", "stop_background_exec")
	mustNotHave(t, agent.Tools, "parent", "write_file")

	if got, want := agent.Tools.Count(), 2; got != want {
		t.Fatalf("parent tool count = %d, want %d (%v)", got, want, agent.Tools.List())
	}

	sub := sm.GetToolRegistry()
	mustHave(t, sub, "subagent", "exec", "read_file")
	mustNotHave(t, sub, "subagent", "spawn", "web_search")
}

func TestSharedTools_AllowlistIdempotent(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "twice",
		Default: true,
		Tools:   []string{"read_file", "exec", "not_registered_yet"},
	})

	agent, _ := registerSharedForTest(t, cfg, "twice")
	first := registeredSorted(agent.Tools)

	mustHave(t, agent.Tools, "parent", "read_file", "exec")

	registry := NewAgentRegistry(cfg)
	ag2, ok := registry.GetAgent("twice")
	if !ok {
		t.Fatal("agent twice missing after rebuild")
	}
	applyToolsAllowlist(ag2.Tools, agentConfigForID(cfg, "twice").Tools, "twice")
	applyToolsAllowlist(ag2.Tools, agentConfigForID(cfg, "twice").Tools, "twice")
	if got := registeredSorted(ag2.Tools); !equalStrings(first, got) {
		t.Fatalf("re-applying the allowlist changed the toolset: %v -> %v", first, got)
	}
}

func TestSharedTools_UnknownAgentKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t)

	agent, _ := registerSharedForTest(t, cfg, "main")
	mustHave(t, agent.Tools, "implicit main", sharedToolNames...)

	if ac := agentConfigForID(cfg, "main"); ac != nil {
		t.Fatalf("agentConfigForID found a config entry for an unconfigured agent: %+v", ac)
	}
}

func TestSharedTools_RegistersEverySharedTool(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})

	agent, _ := registerSharedForTest(t, cfg, "main")
	mustHave(t, agent.Tools, "unrestricted main", sharedToolNames...)
}

func TestSharedTools_SubagentsInheritUpdateMemoryTool(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})

	agent, sm := registerSharedForTest(t, cfg, "main")
	mustHave(t, agent.Tools, "parent", "update_memory")

	sub := sm.GetToolRegistry()
	if sub == nil {
		t.Fatal("subagent manager has no tool registry")
	}
	mustHave(t, sub, "subagent", "update_memory")
}

func TestAgentConfigForID(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "Main", Tools: []string{"read_file"}},
				{ID: "Coder Agent"},
				{ID: "noTools"},
			},
		},
	}

	cases := []struct {
		name    string
		id      string
		wantNil bool
		want    []string
	}{
		{name: "exact", id: "main", want: []string{"read_file"}},
		{name: "raw config id", id: "Main", want: []string{"read_file"}},
		{name: "normalised compound id", id: "coder-agent", want: []string(nil)},
		{name: "empty allowlist", id: "noTools", want: []string(nil)},
		{name: "unknown", id: "ghost", wantNil: true},
		{name: "blank id falls back to default", id: "  ", want: []string{"read_file"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := agentConfigForID(cfg, tc.id)
			if tc.wantNil {
				if ac != nil {
					t.Fatalf("agentConfigForID(%q) = %+v, want nil", tc.id, ac)
				}
				return
			}
			if ac == nil {
				t.Fatalf("agentConfigForID(%q) = nil, want entry", tc.id)
			}
			if !equalStrings(ac.Tools, tc.want) {
				t.Fatalf("agentConfigForID(%q).Tools = %v, want %v", tc.id, ac.Tools, tc.want)
			}
		})
	}

	if agentConfigForID(nil, "main") != nil {
		t.Error("agentConfigForID(nil cfg) must be nil-safe")
	}
}

func TestAllowlistKeeps(t *testing.T) {
	list := func(s ...string) *config.AgentConfig { return &config.AgentConfig{Tools: s} }

	cases := []struct {
		name string
		cfg  *config.AgentConfig
		tool string
		want bool
	}{
		{name: "nil config keeps", cfg: nil, tool: "exec", want: true},
		{name: "nil allowlist keeps", cfg: list(), tool: "exec", want: true},
		{name: "empty allowlist keeps", cfg: list(nil...), tool: "exec", want: true},
		{name: "blank entries keep all", cfg: list("  ", ""), tool: "exec", want: true},
		{name: "listed keeps", cfg: list("read_file", "exec"), tool: "exec", want: true},
		{name: "unlisted removes", cfg: list("read_file"), tool: "exec", want: false},
		{name: "case sensitive", cfg: list("Exec"), tool: "exec", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowlistKeeps(tc.cfg, tc.tool); got != tc.want {
				t.Fatalf("allowlistKeeps(%+v, %q) = %v, want %v", tc.cfg, tc.tool, got, tc.want)
			}
		})
	}
}

func TestRegisterTool_HonoursAllowlist(t *testing.T) {
	cfg := sharedToolsTestConfig(t,
		config.AgentConfig{ID: "main", Default: true},
		config.AgentConfig{ID: "readonly", Tools: []string{"read_file", "cron"}},
	)

	al := NewAgentLoop(cfg, bus.NewMessageBus())

	al.RegisterTool(&mockTool{name: "cron", description: "late registration"})
	al.RegisterTool(&mockTool{name: "secret", description: "late registration, unlisted"})

	unrestricted, ok := al.registry.GetAgent("main")
	if !ok {
		t.Fatal("main agent missing")
	}
	mustHave(t, unrestricted.Tools, "main", "cron", "secret")

	restricted, ok := al.registry.GetAgent("readonly")
	if !ok {
		t.Fatal("readonly agent missing")
	}
	mustHave(t, restricted.Tools, "readonly", "cron")
	mustNotHave(t, restricted.Tools, "readonly", "secret", "exec", "spawn")
}

func TestSharedTools_AllowlistNamingOnlySharedTools(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "searchy",
		Default: true,
		Tools:   []string{"web_search"},
	})

	agent, sm := registerSharedForTest(t, cfg, "searchy")

	mustHave(t, agent.Tools, "parent", "web_search")
	mustNotHave(t, agent.Tools, "parent", "web_fetch", "exec", "spawn", "read_file", "sleep")
	if got, want := agent.Tools.Count(), 1; got != want {
		t.Fatalf("parent tool count = %d, want %d (%v)", got, want, agent.Tools.List())
	}
	mustHave(t, sm.GetToolRegistry(), "subagent", "web_search")
}

func TestSharedTools_AllowlistNoIntersectionKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "typo",
		Default: true,
		Tools:   []string{"read_fiile"},
	})

	unrestricted := sharedToolsTestConfig(t, config.AgentConfig{ID: "plain", Default: true})
	plain, _ := registerSharedForTest(t, unrestricted, "plain")

	agent, _ := registerSharedForTest(t, cfg, "typo")
	mustHave(t, agent.Tools, "typo", "read_file", "exec", "spawn")
	if got, want := agent.Tools.Count(), plain.Tools.Count(); got != want {
		t.Fatalf("zero-intersection allowlist must keep all tools: got %d (%v), want %d",
			got, agent.Tools.List(), want)
	}
}

func TestSyncGroupChatTool_AllowlistBlocksOnReload(t *testing.T) {
	plain := sharedToolsTestConfig(t, config.AgentConfig{ID: "nogroups", Default: true})
	plain.Groups.Enabled = true
	registry := NewAgentRegistry(plain)
	agent, ok := registry.GetAgent("nogroups")
	if !ok {
		t.Fatal("agent nogroups missing")
	}

	gm := group.NewGroupManager(func(string) (group.AgentContext, bool) {
		return group.AgentContext{AgentID: "nogroups"}, true
	}, nil, nil)

	msgBus := bus.NewMessageBus()
	registerSharedToolsForAgent(agent, plain, msgBus, registry, nil, "nogroups",
		make(map[string]*tools.SubagentManager), make(map[string]*tools.BackgroundProcessManager), gm, nil)
	if _, exists := agent.Tools.Get("group_chat"); !exists {
		t.Fatal("precondition: without an allowlist group_chat must be registered")
	}
	agent.Tools.Unregister("group_chat")

	restricted := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "nogroups",
		Default: true,
		Tools:   []string{"read_file", "exec"},
	})
	restricted.Groups.Enabled = true

	syncGroupChatTool(agent, restricted, registry, "nogroups", gm)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("reload sync re-added group_chat for an agent whose allowlist excludes it")
	}

	agent.Tools.Register(&mockTool{name: "group_chat"})
	syncGroupChatTool(agent, restricted, registry, "nogroups", gm)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("reload sync must unregister a group_chat that the allowlist forbids")
	}
}

func TestSyncGroupChatTool_AllowlistAllowsListedAgent(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "groupy",
		Default: true,
		Tools:   []string{"read_file", "group_chat"},
	})
	cfg.Groups.Enabled = true

	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent("groupy")
	if !ok {
		t.Fatal("agent groupy missing")
	}

	gm := group.NewGroupManager(func(string) (group.AgentContext, bool) {
		return group.AgentContext{AgentID: "groupy"}, true
	}, nil, nil)

	syncGroupChatTool(agent, cfg, registry, "groupy", gm)
	mustHave(t, agent.Tools, "groupy", "group_chat")

	syncGroupChatTool(agent, cfg, registry, "groupy", gm)
	mustHave(t, agent.Tools, "groupy", "group_chat")
}
