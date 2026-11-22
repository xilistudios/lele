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
	cfgDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", cfgDir)
	workspace := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.Model = "test-model"
	cfg.Channels.Native.Enabled = true

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

	msgBus := bus.NewMessageBus()
	loop := agent.NewAgentLoopWithStore(cfg, msgBus, nil)
	t.Cleanup(func() { loop.Stop() })

	fixture := channels.NewGatewayWiringFixture(t, cfg, msgBus, loop.GetProvidable())

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
