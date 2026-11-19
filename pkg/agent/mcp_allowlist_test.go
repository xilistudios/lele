// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// Tests for the PER-TOOL allowlist gating of the lazy MCP discovery pair:
// mcpDiscoveryAllowed decides whether a session layer carries list_mcp_tools,
// get_mcp_tool, both or neither, and a reload that tightens only one of them
// must reach the already cached layers (mcpLayerFlags reconciliation).
//
// The bulk loader (load_mcp_tools) is not registered anywhere any more; its
// name survives only as a legacy allowlist alias that keeps BOTH new tools.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// mcpSetMainAllowlist replaces the "main" agent entry of the live config with
// one carrying tools as its allowlist (nil/empty ⇒ no entry, i.e. no
// allowlist). The coordinator and the sync read the config on every pass, so a
// mutation is exactly what a config reload publishes.
func mcpSetMainAllowlist(t *testing.T, tc *toolCoordinatorImpl, allowlist []string) {
	t.Helper()
	cfg := tc.al.cfg()
	if cfg == nil {
		t.Fatal("loop has no config")
	}
	kept := make([]config.AgentConfig, 0, len(cfg.Agents.List)+1)
	for _, entry := range cfg.Agents.List {
		if entry.ID != "main" {
			kept = append(kept, entry)
		}
	}
	if allowlist != nil {
		kept = append(kept, config.AgentConfig{ID: "main", Tools: allowlist})
	}
	cfg.Agents.List = kept
}

// mcpLayerFlagsFor reads the recorded discovery flags of one agent (the map is
// guarded by mcpManagerSet.mu).
func mcpLayerFlagsFor(tc *toolCoordinatorImpl, agentID string) (mcpLayerFlags, bool) {
	tc.mcpManagers.mu.Lock()
	defer tc.mcpManagers.mu.Unlock()
	flags, ok := tc.mcpManagers.layerFlags[agentID]
	return flags, ok
}

// TestMCPDiscoveryAllowed pins the pure gate: which discovery tools survive an
// allowlist, with "load_mcp_tools" acting as the legacy alias for both.
func TestMCPDiscoveryAllowed(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *config.AgentConfig
		wantList bool
		wantGet  bool
	}{
		{name: "no agent config", cfg: nil, wantList: true, wantGet: true},
		{name: "no allowlist", cfg: &config.AgentConfig{ID: "main"}, wantList: true, wantGet: true},
		{
			name:     "blank-only allowlist is no allowlist",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{"", "  "}},
			wantList: true, wantGet: true,
		},
		{
			name:     "list only",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{tools.ListMCPToolsName}},
			wantList: true, wantGet: false,
		},
		{
			name:     "get only",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{tools.GetMCPToolName}},
			wantList: false, wantGet: true,
		},
		{
			name:     "legacy bulk loader alias enables both",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{tools.LoadMCPToolsName}},
			wantList: true, wantGet: true,
		},
		{
			name:     "both new tools",
			cfg:      &config.AgentConfig{ID: "main", Tools: mcpDiscoveryToolNames},
			wantList: true, wantGet: true,
		},
		{
			name:     "unrelated tool only",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{"read_file"}},
			wantList: false, wantGet: false,
		},
		{
			name:     "whitespace around a listed name is ignored",
			cfg:      &config.AgentConfig{ID: "main", Tools: []string{"  " + tools.ListMCPToolsName + "  "}},
			wantList: true, wantGet: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			list, get := mcpDiscoveryAllowed(tc.cfg)
			if list != tc.wantList || get != tc.wantGet {
				t.Errorf("mcpDiscoveryAllowed = (list=%v, get=%v), want (%v, %v)",
					list, get, tc.wantList, tc.wantGet)
			}
		})
	}
}

// TestMCPSessionTools_PerToolAllowlist is the registration matrix: every
// allowlist shape yields a session layer carrying exactly the permitted
// discovery tools, never the bulk loader, and never anything in the base
// registry.
func TestMCPSessionTools_PerToolAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		allowlist []string
		wantList  bool
		wantGet   bool
	}{
		{name: "default (no allowlist)", allowlist: nil, wantList: true, wantGet: true},
		{name: "list only", allowlist: []string{tools.ListMCPToolsName}, wantList: true, wantGet: false},
		{name: "get only", allowlist: []string{tools.GetMCPToolName}, wantList: false, wantGet: true},
		{name: "legacy alias", allowlist: []string{tools.LoadMCPToolsName}, wantList: true, wantGet: true},
		{
			name:      "unrelated tool only",
			allowlist: []string{"read_file"},
			wantList:  false,
			wantGet:   false,
		},
		{
			name:      "list plus an unrelated tool",
			allowlist: []string{tools.ListMCPToolsName, "read_file"},
			wantList:  true,
			wantGet:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, coord, agent, _ := e2eMCPWiring(t)
			mcpSetMainAllowlist(t, coord, tc.allowlist)
			baseCount := agent.Tools.Count()

			view := coord.sessionTools(agent, "chat-a")

			if !tc.wantList && !tc.wantGet {
				// Neither tool survives: no layer at all, the session uses the
				// agent's own registry.
				if view != agent.Tools {
					t.Fatalf("session view must be the base registry when neither discovery tool is allowed, have %v", view.List())
				}
				for _, name := range mcpDiscoveryToolNames {
					if _, ok := view.Get(name); ok {
						t.Errorf("%s must not be offered by the base registry", name)
					}
				}
				return
			}

			if !view.IsScoped() {
				t.Fatalf("the session view must be a scoped layer, have %v", view.List())
			}
			wantCount := baseCount
			for _, entry := range []struct {
				name string
				want bool
			}{
				{tools.ListMCPToolsName, tc.wantList},
				{tools.GetMCPToolName, tc.wantGet},
			} {
				_, ok := view.Get(entry.name)
				if ok != entry.want {
					t.Errorf("%s offered = %v, want %v (session tools: %v)", entry.name, ok, entry.want, view.List())
				}
				if entry.want {
					wantCount++
				}
			}
			if view.Count() != wantCount {
				t.Errorf("session layer Count() = %d, want %d (base + the allowed discovery tools)",
					view.Count(), wantCount)
			}
			// The retired bulk loader is never part of a layer.
			if _, ok := view.Get(tools.LoadMCPToolsName); ok {
				t.Errorf("the bulk loader must not be registered (session tools: %v)", view.List())
			}
			// Built-ins stay reachable through the layer.
			if _, ok := view.Get("read_file"); !ok {
				t.Errorf("the session layer must fall through to the base registry, have %v", view.List())
			}
			// Nothing leaked into the agent-wide registry.
			if got := agent.Tools.Count(); got != baseCount {
				t.Errorf("base registry Count() = %d, want %d (untouched)", got, baseCount)
			}
			for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames...) {
				if _, ok := agent.Tools.Get(name); ok {
					t.Errorf("%s must never reach the agent-wide registry", name)
				}
			}
		})
	}
}

// TestMCPSync_NeitherAllowedDropsSessionLayers is the sync half of the gate: an
// allowlist that keeps neither discovery tool drops the live layers (they hold
// the discovery tools AND everything loaded through them).
func TestMCPSync_NeitherAllowedDropsSessionLayers(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)

	view := coord.sessionTools(agent, "chat-a")
	if !view.IsScoped() {
		t.Fatal("precondition: the session layer must exist before tightening")
	}
	mcpLoadTools(t, coord, agent, "chat-a", "demo", mcpDemoTools...)
	if got := coord.mcpManagers.layers.len(); got != 1 {
		t.Fatalf("expected exactly 1 live session layer, have %d", got)
	}

	mcpSetMainAllowlist(t, coord, []string{"read_file"})
	coord.syncMCPTools()

	if got := coord.mcpManagers.layers.len(); got != 0 {
		t.Errorf("%d session layers survived an allowlist that keeps neither discovery tool", got)
	}
	if got := coord.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("after the tightening the session must fall back to the base registry")
	}
	if flags, ok := mcpLayerFlagsFor(coord, agent.ID); ok {
		t.Errorf("retired flags must be forgotten, still recorded %+v", flags)
	}
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be registered anywhere after the tightening", name)
		}
	}
}

// TestMCPSessionTools_PartialTighteningRebuildsLayer is THE per-tool reload
// regression: layers are cached per agent × session and survive a config
// reload, so dropping get_mcp_tool from the allowlist while keeping
// list_mcp_tools must reach the cached layer (flag mismatch ⇒ rebuild), and the
// same must hold in the other direction.
func TestMCPSessionTools_PartialTighteningRebuildsLayer(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)
	ctx := context.Background()

	// Both tools allowed by default; a load leaves a remote tool in the layer.
	first := coord.sessionTools(agent, "chat-a")
	mcpLoadTools(t, coord, agent, "chat-a", "demo", mcpDemoTools...)
	if _, ok := first.Get("mcp_demo_echo"); !ok {
		t.Fatal("precondition: the loaded remote tool must be in the layer")
	}
	if flags, ok := mcpLayerFlagsFor(coord, agent.ID); !ok || !flags.list || !flags.get {
		t.Fatalf("recorded flags = %+v (present=%v), want both enabled", flags, ok)
	}

	// Tighten: list stays, get goes.
	mcpSetMainAllowlist(t, coord, []string{tools.ListMCPToolsName})
	coord.syncMCPTools()

	tightened := coord.sessionTools(agent, "chat-a")
	if tightened == first {
		t.Fatal("a partially tightened allowlist must rebuild the session layer, not reuse it")
	}
	if !tightened.IsScoped() {
		t.Fatal("list_mcp_tools is still allowed, so the layer must still exist")
	}
	if _, ok := tightened.Get(tools.ListMCPToolsName); !ok {
		t.Errorf("list_mcp_tools must still be offered, have %v", tightened.List())
	}
	if _, ok := tightened.Get(tools.GetMCPToolName); ok {
		t.Errorf("get_mcp_tool must be gone after the tightening, have %v", tightened.List())
	}
	// The rebuild starts from a clean layer: what the removed tool had loaded
	// goes with it (nothing can load remote tools into this session any more).
	if _, ok := tightened.Get("mcp_demo_echo"); ok {
		t.Error("a rebuilt layer must not keep tools loaded through the revoked get_mcp_tool")
	}
	if flags, ok := mcpLayerFlagsFor(coord, agent.ID); !ok || !flags.list || flags.get {
		t.Errorf("recorded flags = %+v (present=%v), want list only", flags, ok)
	}
	// list_mcp_tools still works through the rebuilt layer.
	listTool := mcpSessionTool(t, coord, agent, "chat-a", tools.ListMCPToolsName)
	res := listTool.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("list_mcp_tools after the tightening failed: %s", res.ForLLM)
	}
	for _, name := range mcpDemoNamespaced {
		if !strings.Contains(res.ForLLM, name) {
			t.Errorf("listing lacks %s:\n%s", name, res.ForLLM)
		}
	}

	// Loosen again: get comes back, list was never removed.
	mcpSetMainAllowlist(t, coord, mcpDiscoveryToolNames)
	coord.syncMCPTools()

	restored := coord.sessionTools(agent, "chat-a")
	if restored == tightened {
		t.Fatal("restoring get_mcp_tool must rebuild the session layer too")
	}
	for _, name := range mcpDiscoveryToolNames {
		if _, ok := restored.Get(name); !ok {
			t.Errorf("%s must be offered again after the allowlist was loosened, have %v", name, restored.List())
		}
	}
	if flags, ok := mcpLayerFlagsFor(coord, agent.ID); !ok || !flags.list || !flags.get {
		t.Errorf("recorded flags = %+v (present=%v), want both enabled again", flags, ok)
	}
	// And the restored layer is functional end to end.
	mcpLoadTools(t, coord, agent, "chat-a", "demo", "echo")
	if _, ok := restored.Get("mcp_demo_echo"); !ok {
		t.Error("get_mcp_tool must be able to register remote tools again")
	}

	// The other direction: get stays, list goes.
	mcpSetMainAllowlist(t, coord, []string{tools.GetMCPToolName})
	coord.syncMCPTools()

	getOnly := coord.sessionTools(agent, "chat-a")
	if _, ok := getOnly.Get(tools.GetMCPToolName); !ok {
		t.Errorf("get_mcp_tool must still be offered, have %v", getOnly.List())
	}
	if _, ok := getOnly.Get(tools.ListMCPToolsName); ok {
		t.Errorf("list_mcp_tools must be gone after the tightening, have %v", getOnly.List())
	}
}

// TestMCPGetTool_RegistersOnlyRequestedTool pins the per-tool granularity of
// the load path: only the tools named in the call reach the layer, and only the
// caller's session sees them.
func TestMCPGetTool_RegistersOnlyRequestedTool(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)
	ctx := context.Background()

	layerA := coord.sessionTools(agent, "chat-a")
	before := layerA.Count()

	mcpLoadTools(t, coord, agent, "chat-a", "demo", "echo")

	if _, ok := layerA.Get("mcp_demo_echo"); !ok {
		t.Errorf("the requested tool must be registered, have %v", layerA.List())
	}
	if _, ok := layerA.Get("mcp_demo_summary"); ok {
		t.Error("a tool that was not requested must not be registered (no bulk load)")
	}
	if got, want := layerA.Count(), before+1; got != want {
		t.Errorf("layer Count() = %d, want %d (exactly one new tool)", got, want)
	}

	// The registered remote tool is callable through the layer.
	out := layerA.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("remote call = %+v, want fake:echo", out)
	}

	// Another session of the same agent sees none of it.
	layerB := coord.sessionTools(agent, "chat-b")
	if _, ok := layerB.Get("mcp_demo_echo"); ok {
		t.Error("session B must not see a tool session A loaded")
	}
	if _, ok := agent.Tools.Get("mcp_demo_echo"); ok {
		t.Error("the loaded tool leaked into the agent-wide registry")
	}
}

// TestMCPListTool_RegistersNothing pins that discovery is free: listing a
// server's tools never touches the registry, neither in the session layer nor
// in the base registry.
func TestMCPListTool_RegistersNothing(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)
	ctx := context.Background()

	layer := coord.sessionTools(agent, "chat-a")
	before := layer.Count()
	baseBefore := agent.Tools.Count()

	listTool := mcpSessionTool(t, coord, agent, "chat-a", tools.ListMCPToolsName)
	res := listTool.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("list_mcp_tools failed: %s", res.ForLLM)
	}
	// The page carries the NAMES (no schemas) and a pointer to get_mcp_tool.
	for _, name := range mcpDemoNamespaced {
		if !strings.Contains(res.ForLLM, name) {
			t.Errorf("listing lacks %s:\n%s", name, res.ForLLM)
		}
	}
	if strings.Contains(res.ForLLM, "inputSchema") {
		t.Errorf("listing must not carry schemas:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Call get_mcp_tool with") {
		t.Errorf("listing must point at get_mcp_tool:\n%s", res.ForLLM)
	}

	if got := layer.Count(); got != before {
		t.Errorf("layer Count() = %d after a listing, want %d (nothing registered)", got, before)
	}
	if got := agent.Tools.Count(); got != baseBefore {
		t.Errorf("base registry Count() = %d after a listing, want %d", got, baseBefore)
	}
	for _, name := range mcpDemoNamespaced {
		if _, ok := layer.Get(name); ok {
			t.Errorf("%s must not be registered by list_mcp_tools", name)
		}
	}
}
