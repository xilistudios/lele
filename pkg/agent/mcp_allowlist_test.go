package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

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
		{
			name:      "blank-only allowlist is no allowlist",
			allowlist: []string{"", "  "},
			wantList:  true,
			wantGet:   true,
		},
		{
			name:      "whitespace around a listed name is ignored",
			allowlist: []string{"  " + tools.ListMCPToolsName + "  "},
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
				if view != agent.Tools {
					t.Fatalf("session view must be the base registry when neither discovery tool is allowed, have %v", view.List())
				}
				for _, name := range mcpDiscoveryToolNames() {
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
			if _, ok := view.Get(tools.LoadMCPToolsName); ok {
				t.Errorf("the bulk loader must not be registered (session tools: %v)", view.List())
			}
			if _, ok := view.Get("read_file"); !ok {
				t.Errorf("the session layer must fall through to the base registry, have %v", view.List())
			}
			if got := agent.Tools.Count(); got != baseCount {
				t.Errorf("base registry Count() = %d, want %d (untouched)", got, baseCount)
			}
			for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
				if _, ok := agent.Tools.Get(name); ok {
					t.Errorf("%s must never reach the agent-wide registry", name)
				}
			}
		})
	}
}

func TestMCPSync_NeitherAllowedDropsSessionLayers(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)

	view := coord.sessionTools(agent, "chat-a")
	if !view.IsScoped() {
		t.Fatal("precondition: the session layer must exist before tightening")
	}
	mcpLoadTools(t, coord, agent, "chat-a", "demo", mcpDemoTools()...)
	if _, ok := view.Get("mcp_demo_echo"); !ok {
		t.Fatal("precondition: the loaded remote tool must be in the layer")
	}

	mcpSetMainAllowlist(t, coord, []string{"read_file"})
	coord.syncMCPTools()

	sessionView := coord.sessionTools(agent, "chat-a")
	if sessionView != agent.Tools {
		t.Errorf("after the tightening the session must fall back to the base registry, have %v", sessionView.List())
	}
	for _, name := range append(append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...), mcpDemoNamespaced()...) {
		if _, ok := sessionView.Get(name); ok {
			t.Errorf("%s must not be offered after the tightening", name)
		}
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be registered anywhere after the tightening", name)
		}
	}
	if _, ok := sessionView.Get("read_file"); !ok {
		t.Errorf("the base registry must still serve built-ins, have %v", sessionView.List())
	}
}

func TestMCPSessionTools_PartialTighteningRebuildsLayer(t *testing.T) {
	_, coord, agent, _ := e2eMCPWiring(t)
	ctx := context.Background()

	first := coord.sessionTools(agent, "chat-a")
	mcpLoadTools(t, coord, agent, "chat-a", "demo", mcpDemoTools()...)
	if _, ok := first.Get("mcp_demo_echo"); !ok {
		t.Fatal("precondition: the loaded remote tool must be in the layer")
	}

	mcpSetMainAllowlist(t, coord, []string{tools.ListMCPToolsName})
	coord.syncMCPTools()

	tightened := coord.sessionTools(agent, "chat-a")
	if !tightened.IsScoped() {
		t.Fatal("list_mcp_tools is still allowed, so the layer must still exist")
	}
	if _, ok := tightened.Get(tools.ListMCPToolsName); !ok {
		t.Errorf("list_mcp_tools must still be offered, have %v", tightened.List())
	}
	if _, ok := tightened.Get(tools.GetMCPToolName); ok {
		t.Errorf("get_mcp_tool must be gone after the tightening, have %v", tightened.List())
	}
	if _, ok := tightened.Get("mcp_demo_echo"); ok {
		t.Error("a rebuilt layer must not keep tools loaded through the revoked get_mcp_tool")
	}
	listTool := mcpSessionTool(t, coord, agent, "chat-a", tools.ListMCPToolsName)
	res := listTool.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("list_mcp_tools after the tightening failed: %s", res.ForLLM)
	}
	for _, name := range mcpDemoNamespaced() {
		if !strings.Contains(res.ForLLM, name) {
			t.Errorf("listing lacks %s:\n%s", name, res.ForLLM)
		}
	}

	mcpSetMainAllowlist(t, coord, mcpDiscoveryToolNames())
	coord.syncMCPTools()

	restored := coord.sessionTools(agent, "chat-a")
	for _, name := range mcpDiscoveryToolNames() {
		if _, ok := restored.Get(name); !ok {
			t.Errorf("%s must be offered again after the allowlist was loosened, have %v", name, restored.List())
		}
	}
	mcpLoadTools(t, coord, agent, "chat-a", "demo", "echo")
	if _, ok := restored.Get("mcp_demo_echo"); !ok {
		t.Error("get_mcp_tool must be able to register remote tools again")
	}

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

func TestMCPSync_PromptMatchesAllowlist(t *testing.T) {
	cases := []struct {
		name     string
		allow    []string
		wantList bool
		wantGet  bool
	}{
		{name: "no allowlist", allow: nil, wantList: true, wantGet: true},
		{name: "list only", allow: []string{tools.ListMCPToolsName}, wantList: true, wantGet: false},
		{name: "get only", allow: []string{tools.GetMCPToolName}, wantList: false, wantGet: true},
		{name: "legacy alias", allow: []string{tools.LoadMCPToolsName}, wantList: true, wantGet: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, coord, agent, _ := e2eMCPWiring(t)
			mcpSetMainAllowlist(t, coord, tc.allow)
			coord.syncMCPTools()

			sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt())
			if !strings.Contains(sec, "- **demo**") {
				t.Fatalf("the configured server must stay in the section:\n%s", sec)
			}
			if got := strings.Contains(sec, "`list_mcp_tools`"); got != tc.wantList {
				t.Errorf("section mentions list_mcp_tools = %v, want %v:\n%s", got, tc.wantList, sec)
			}
			if got := strings.Contains(sec, "`get_mcp_tool`"); got != tc.wantGet {
				t.Errorf("section mentions get_mcp_tool = %v, want %v:\n%s", got, tc.wantGet, sec)
			}
		})
	}
}

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

	out := layerA.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("remote call = %+v, want fake:echo", out)
	}

	layerB := coord.sessionTools(agent, "chat-b")
	if _, ok := layerB.Get("mcp_demo_echo"); ok {
		t.Error("session B must not see a tool session A loaded")
	}
	if _, ok := agent.Tools.Get("mcp_demo_echo"); ok {
		t.Error("the loaded tool leaked into the agent-wide registry")
	}
}

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
	for _, name := range mcpDemoNamespaced() {
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
	for _, name := range mcpDemoNamespaced() {
		if _, ok := layer.Get(name); ok {
			t.Errorf("%s must not be registered by list_mcp_tools", name)
		}
	}
}
