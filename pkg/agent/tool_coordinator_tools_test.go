// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// B2b: the per-agent `tools:` allowlist must also cover the SHARED tools that
// registerSharedToolsForAgent layers on top of the instance registry (web,
// hardware, send_file, exec, background exec, spawn + subagent management,
// sleep, group_chat). Without the second pass at the end of that function an
// agent configured with tools: ["read_file"] ended up with exec/spawn/web
// anyway, which is the gap these tests pin closed. They also pin the two
// properties the fix relies on: idempotence (a nil allowlist is a no-op) and
// inheritance (the subagent clone is taken AFTER the filter, so a restricted
// parent cannot leak tools through its subagents).

package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/tools"
)

// --- harness ----------------------------------------------------------------
//
// sharedToolsTestConfig builds a config with one agent per entry and web_search
// enabled, so web_search is a real shared registration that the allowlist must
// be able to remove. Providers is left nil on purpose: ResolveModelAlias and the
// provider-creation paths are nil-safe and fall back gracefully, which keeps the
// harness free of any credential or network dependency.
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

// registerSharedForTest runs registerSharedToolsForAgent for one agent of the
// registry, mirroring the production wiring in registerSharedTools (loop.go
// calls it once per registry.ListAgentIDs()). It returns the agent instance and
// the SubagentManager the function built, so tests can inspect both the parent
// toolset and what its subagents inherit.
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

// mustHave asserts the tool is present in the registry.
func mustHave(t *testing.T, r *tools.ToolRegistry, label string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := r.Get(n); !ok {
			t.Errorf("%s: tool %q missing, have %v", label, n, r.List())
		}
	}
}

// mustNotHave asserts the tool is absent from the registry.
func mustNotHave(t *testing.T, r *tools.ToolRegistry, label string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, ok := r.Get(n); ok {
			t.Errorf("%s: tool %q must not be available, have %v", label, n, r.List())
		}
	}
}

// sharedToolNames are the tools registerSharedToolsForAgent adds on top of the
// instance registry. Every test that restricts an agent must see all of them
// gone (subject to the per-test allowlist).
var sharedToolNames = []string{
	"web_search", "web_fetch", "i2c", "spi", "send_file",
	"exec", "list_background_execs", "get_background_exec_output", "stop_background_exec",
	"spawn", "wait_for_subagent", "list_active_subagents", "cancel_subagent", "sleep",
}

// --- tests ------------------------------------------------------------------

// TestSharedTools_AllowlistRemovesSharedTools is the gap itself: an agent
// declaring only the two read-only file tools must not end up with any shared
// tool after the coordinator has run.
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

	// The subagent clone is taken after the filter, so a restricted parent must
	// not hand exec/spawn to its subagents either.
	sub := sm.GetToolRegistry()
	if sub == nil {
		t.Fatal("subagent manager has no tool registry")
	}
	mustHave(t, sub, "subagent", "read_file", "list_dir")
	mustNotHave(t, sub, "subagent", sharedToolNames...)

	// ContextBuilder must see the filtered registry, not the pre-filter one
	// (same package, so the unexported field is reachable: there is no getter).
	if agent.ContextBuilder.tools == nil {
		t.Fatal("ContextBuilder lost its tool registry")
	} else if _, ok := agent.ContextBuilder.tools.Get("exec"); ok {
		t.Error("ContextBuilder registry still exposes exec after the allowlist")
	}
}

// TestSharedTools_EmptyAllowlistKeepsAll pins the no-op branch: an agent with no
// `tools:` entry keeps every shared tool (default behaviour must not regress).
func TestSharedTools_EmptyAllowlistKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t,
		config.AgentConfig{ID: "main", Default: true},         // Tools nil
		config.AgentConfig{ID: "explicit", Tools: []string{}}, // Tools empty
	)

	for _, id := range []string{"main", "explicit"} {
		agent, sm := registerSharedForTest(t, cfg, id)
		mustHave(t, agent.Tools, id, sharedToolNames...)
		mustHave(t, agent.Tools, id, "read_file", "write_file", "exec")
		// secret/i2c/spi are registered unconditionally by the coordinator
		// (secret only when a keyring service exists, which the harness has
		// none of), so they are not asserted here.
		if sub := sm.GetToolRegistry(); sub == nil {
			t.Fatalf("%s: no subagent tool registry", id)
		} else {
			mustHave(t, sub, id+" subagent", "exec", "spawn", "web_fetch")
		}
	}
}

// TestSharedTools_AllowlistPartialOverlap checks a mixed allowlist: the shared
// tool that is listed survives, the ones that are not do not, and the instance
// tools stay untouched.
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

	// exec is inherited by subagents (it is in the allowlist), spawn is not.
	sub := sm.GetToolRegistry()
	mustHave(t, sub, "subagent", "exec", "read_file")
	mustNotHave(t, sub, "subagent", "spawn", "web_search")
}

// TestSharedTools_AllowlistIsIdempotent covers the reload path for a recreated
// agent: NewAgentInstance already filtered the base registry and the coordinator
// filters again. Running the whole registration twice must converge on the same
// toolset (a second pass may not remove tools the allowlist keeps).
func TestSharedTools_AllowlistIdempotent(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "twice",
		Default: true,
		Tools:   []string{"read_file", "exec", "not_registered_yet"},
	})

	agent, _ := registerSharedForTest(t, cfg, "twice")
	first := registeredSorted(agent.Tools)

	// Unknown names are warnings, never an error: the two known ones must be kept.
	mustHave(t, agent.Tools, "parent", "read_file", "exec")

	// Second pass over the same instance, as a reload of a recreated agent does.
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

// TestSharedTools_UnknownAgentKeepsAll pins the nil-agentCfg branch: an agent id
// with no entry in agents.list (the implicit "main" of a config without agents)
// must keep every tool instead of being filtered to nothing.
func TestSharedTools_UnknownAgentKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t) // no agents.list at all -> implicit "main"

	agent, _ := registerSharedForTest(t, cfg, "main")
	mustHave(t, agent.Tools, "implicit main", sharedToolNames...)

	if ac := agentConfigForID(cfg, "main"); ac != nil {
		t.Fatalf("agentConfigForID found a config entry for an unconfigured agent: %+v", ac)
	}
}

// TestSharedTools_RegistersEverySharedTool is the guard for the harness itself:
// if the coordinator ever stops registering one of the names in
// sharedToolNames, the "removed by allowlist" assertions above would pass for
// the wrong reason.
func TestSharedTools_RegistersEverySharedTool(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: "main", Default: true})

	agent, _ := registerSharedForTest(t, cfg, "main")
	mustHave(t, agent.Tools, "unrestricted main", sharedToolNames...)
}

// --- agentConfigForID / allowlistKeeps unit tests ---------------------------

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
		// The registry keys instances by NormalizeAgentID, so a raw config id
		// must find the same entry (idempotent normalisation).
		{name: "raw config id", id: "Main", want: []string{"read_file"}},
		{name: "normalised compound id", id: "coder-agent", want: []string(nil)},
		{name: "empty allowlist", id: "noTools", want: []string(nil)},
		{name: "unknown", id: "ghost", wantNil: true},
		// A blank id normalises to the routing default ("main"), so it resolves
		// to the entry above — the same way the registry keys its instances.
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

// TestRegisterTool_HonoursAllowlist covers the late-registration path used by
// the gateway for cron: a tool added after startup must not be handed to an
// agent whose allowlist excludes it.
func TestRegisterTool_HonoursAllowlist(t *testing.T) {
	cfg := sharedToolsTestConfig(t,
		config.AgentConfig{ID: "main", Default: true},
		config.AgentConfig{ID: "readonly", Tools: []string{"read_file", "cron"}},
	)

	al := NewAgentLoop(cfg, bus.NewMessageBus())

	// "cron" is listed for readonly, "secret" is not: registering both proves the
	// late path filters instead of either blocking everything or nothing.
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

// TestSharedTools_AllowlistNamingOnlySharedTools pins the payoff of running the
// filter AFTER the shared registrations: a name that does not exist yet when
// NewAgentInstance builds the registry (web_search lives in the coordinator) must
// still be selectable. On its own the instance-level pass sees zero intersection
// and keeps everything, so without the second pass this configuration could never
// be narrowed.
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
	// The single allowed tool is inherited by subagents.
	mustHave(t, sm.GetToolRegistry(), "subagent", "web_search")
}

// TestSharedTools_AllowlistNoIntersectionKeepsAll documents the shared
// fail-safe with a name no path ever registers: the agent must not be bricked
// down to zero tools. Both enforcement points (instance + coordinator) agree.
func TestSharedTools_AllowlistNoIntersectionKeepsAll(t *testing.T) {
	cfg := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "typo",
		Default: true,
		Tools:   []string{"read_fiile"}, // typo on purpose
	})

	// Reference: the same agent with no allowlist at all.
	unrestricted := sharedToolsTestConfig(t, config.AgentConfig{ID: "plain", Default: true})
	plain, _ := registerSharedForTest(t, unrestricted, "plain")

	agent, _ := registerSharedForTest(t, cfg, "typo")
	mustHave(t, agent.Tools, "typo", "read_file", "exec", "spawn")
	if got, want := agent.Tools.Count(), plain.Tools.Count(); got != want {
		t.Fatalf("zero-intersection allowlist must keep all tools: got %d (%v), want %d",
			got, agent.Tools.List(), want)
	}
}

// TestSyncGroupChatTool_AllowlistBlocksOnReload covers the reload path, which is
// the sneaky one: updateSharedToolsForAgent early-returns when "spawn" is
// already present and re-syncs ONLY group_chat. If the sync ignored the
// allowlist, a reload would hand group_chat back to an agent whose allowlist had
// it removed at first registration. Groups are enabled here, so the feature gate
// alone would happily register it — only the allowlist check stops it.
func TestSyncGroupChatTool_AllowlistBlocksOnReload(t *testing.T) {
	// Agent with NO allowlist: the instance-level filter keeps everything, so the
	// only thing that can stop group_chat here is the check inside the sync
	// itself. That isolates the reload path from the (already covered)
	// registration-time filtering.
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
	agent.Tools.Unregister("group_chat") // what the allowlist pass would have done

	// Reload config: now the agent declares an allowlist without group_chat.
	restricted := sharedToolsTestConfig(t, config.AgentConfig{
		ID:      "nogroups",
		Default: true,
		Tools:   []string{"read_file", "exec"},
	})
	restricted.Groups.Enabled = true

	// The tool was removed, sync must not add it back.
	syncGroupChatTool(agent, restricted, registry, "nogroups", gm)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("reload sync re-added group_chat for an agent whose allowlist excludes it")
	}

	// Even a stale registration must be dropped rather than left alone.
	agent.Tools.Register(&mockTool{name: "group_chat"})
	syncGroupChatTool(agent, restricted, registry, "nogroups", gm)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("reload sync must unregister a group_chat that the allowlist forbids")
	}
}

// TestSyncGroupChatTool_AllowlistAllowsListedAgent is the other side of the gate:
// an agent whose allowlist names group_chat must get it (and keep it across
// reloads), otherwise the check would simply be disabling the tool for everyone.
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

	// Idempotent: a second sync (reload) keeps it registered.
	syncGroupChatTool(agent, cfg, registry, "groupy", gm)
	mustHave(t, agent.Tools, "groupy", "group_chat")
}
