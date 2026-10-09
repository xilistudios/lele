// Regression test for the gateway MCP 500 defect, built on the PRODUCTION
// wiring: cmd/lele/gateway.go:225 hands channels.NewManager the
// agentLoop.GetProvidable() facade, and pkg/channels/rest_mcp.go resolves its
// MCP inventory through a runtime type assertion for MCPPathsFor. When the
// facade does not forward MCPPathsFor, that assertion fails for every agent in
// every real gateway: GET/POST /api/v1/mcp* all answer 500 mcp_unavailable,
// while the suite stays green because every other test injects a fake loop
// that implements the capability.
//
// The test therefore builds a REAL *agent.AgentLoop (the gateway constructor),
// wires it through GetProvidable() → channels.NewManager exactly like
// gateway.go does (see gateway_wiring_export_test.go, which also asserts
// channel.agentLoop satisfies mcpPathsSource), and asserts the user-visible
// route: GET /api/v1/mcp must be 200 with servers reflecting the mcp.json
// fixture, not 500.
package channels_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
)

func TestGatewayWiring_MCPInventoryLive(t *testing.T) {
	// Isolate the mcp.json layers this agent reads: the global layer lives in
	// LeleDir (LELE_CONFIG_DIR), the agent layer in the agent workspace. The
	// project layer (<cwd>/.lele/mcp.json) stays absent in this checkout.
	cfgDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", cfgDir)
	workspace := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Channels.Native.Enabled = true

	// One fixture server per layer, distinct names, so the merged inventory
	// proves both roots were read through the facade.
	writeLayer := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
	writeLayer(filepath.Join(cfgDir, "mcp.json"),
		`{"mcpServers": {"wiring-global": {"command": "global-cmd"}}}`)
	writeLayer(filepath.Join(workspace, "mcp.json"),
		`{"mcpServers": {"wiring-agent": {"command": "agent-cmd"}}}`)

	// The same constructor the gateway uses (store nil = documented JSON
	// fallback, identical for the read path under test).
	msgBus := bus.NewMessageBus()
	loop := agent.NewAgentLoopWithStore(cfg, msgBus, nil)
	t.Cleanup(func() { loop.Stop() })

	// Production wiring: GetProvidable() facade → channels.NewManager →
	// NativeChannel routes. The fixture asserts the mcpPathsSource seam on the
	// value the router will assert against at request time.
	fixture := channels.NewGatewayWiringFixture(t, cfg, msgBus, loop.GetProvidable())

	// The user-visible route: before the facade forwarded MCPPathsFor this is
	// 500 mcp_unavailable for every agent.
	status, body := fixture.Get(t, "/api/v1/mcp?agent_id=main")
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/mcp?agent_id=main = %d %s, want 200 — the facade does not "+
			"forward MCPPathsFor, so the real gateway answers 500 mcp_unavailable for every "+
			"MCP route", status, body)
	}
	var inv channels.MCPInventoryResponse
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", body, err)
	}
	if inv.AgentID != "main" {
		t.Errorf("GET /api/v1/mcp agent_id = %q, want %q", inv.AgentID, "main")
	}

	got := make(map[string]bool, len(inv.Servers))
	for _, s := range inv.Servers {
		got[s.Name] = true
	}
	if len(inv.Servers) != 2 {
		t.Errorf("GET /api/v1/mcp servers = %v, want exactly the 2 fixture servers", got)
	}
	for _, want := range []string{"wiring-agent", "wiring-global"} {
		if !got[want] {
			t.Errorf("GET /api/v1/mcp servers = %v, want %q present (fixture mcp.json layers not read through the facade)", got, want)
		}
	}

	// The ok/argument contract across the SAME seam: an agent that does not
	// exist must be 404 agent_not_found (rest_mcp.go:96-99), not 200 and not
	// 500. Two review mutants survived without this check: M2 (forward
	// MCPPathsFor but hardcode "main", ignoring agentID) and M5 (swallow ok and
	// always return true) both answer 200 here, so this subtest is what makes
	// an ignored agentID or a swallowed ok fail.
	t.Run("unknown agent is 404 agent_not_found", func(t *testing.T) {
		status, body := fixture.Get(t, "/api/v1/mcp?agent_id=ghost")
		if status != http.StatusNotFound {
			t.Fatalf("GET /api/v1/mcp?agent_id=ghost = %d %s, want 404 — the facade must pass "+
				"agentID through to MCPPathsFor and forward ok unchanged; an ignored agentID or a "+
				"swallowed ok answers 200 for an agent that does not exist", status, body)
		}
		var apiErr channels.APIError
		if err := json.Unmarshal(body, &apiErr); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", body, err)
		}
		if apiErr.Code != "agent_not_found" {
			t.Errorf("GET /api/v1/mcp?agent_id=ghost code = %q, want %q (body %s)",
				apiErr.Code, "agent_not_found", body)
		}
	})
}
