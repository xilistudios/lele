package channels

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/security"
)

type GatewayWiringFixture struct {
	Channel *NativeChannel
	Server  *httptest.Server
	Token   string
}

func NewGatewayWiringFixture(t *testing.T, cfg *config.Config, msgBus *bus.MessageBus, prov AgentProvidable) *GatewayWiringFixture {
	t.Helper()

	m, err := NewManager(cfg, msgBus, prov, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ch, ok := m.GetChannel(ChannelName)
	if !ok {
		t.Fatalf("GetChannel(%q) not found", ChannelName)
	}
	channel, ok := ch.(*NativeChannel)
	if !ok {
		t.Fatalf("GetChannel(%q) = %T, want *NativeChannel", ChannelName, ch)
	}

	for _, seam := range []struct {
		name   string
		site   string
		checks func(AgentProvidable) bool
		breaks string
	}{
		{
			name:   "mcpPathsSource",
			site:   "rest_mcp.go:91",
			checks: func(p AgentProvidable) bool { _, ok := p.(mcpPathsSource); return ok },
			breaks: "every MCP route 500s with mcp_unavailable for every agent",
		},
		{
			name:   "agentSkillsSource",
			site:   "rest_agent_skills.go:82",
			checks: func(p AgentProvidable) bool { _, ok := p.(agentSkillsSource); return ok },
			breaks: "per-agent skills endpoints silently fall back to the channel default loader",
		},
		{
			name:   "customCommandProvider",
			site:   "rest_commands.go:105",
			checks: func(p AgentProvidable) bool { _, ok := p.(customCommandProvider); return ok },
			breaks: "the slash-command palette silently loses every harness custom command",
		},
		{
			name:   "harnessCommandInvalidator",
			site:   "rest_agent_commands.go:868",
			checks: func(p AgentProvidable) bool { _, ok := p.(harnessCommandInvalidator); return ok },
			breaks: "REST command writes silently skip invalidation and the dispatcher serves a stale registry for up to harnessRefreshTTL (30s)",
		},
	} {
		if !seam.checks(channel.agentLoop) {
			t.Fatalf("the gateway hands NewManager the AgentProvidable facade; if the facade "+
				"stops forwarding %s (asserted at %s), %s (facade no longer satisfies that seam)",
				seam.name, seam.site, seam.breaks)
		}
	}

	mux := http.NewServeMux()
	channel.RegisterRoutes(mux)
	server := httptest.NewServer(security.Middleware()(mux))
	t.Cleanup(server.Close)

	pending, err := channel.auth.GeneratePIN("Gateway Wiring Test")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}
	_, token, _, err := channel.auth.PairWithPIN(pending.PIN, "Gateway Wiring Test")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	return &GatewayWiringFixture{Channel: channel, Server: server, Token: token}
}

func (f *GatewayWiringFixture) Get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.Server.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+f.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do(%s): %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", path, err)
	}
	return resp.StatusCode, body
}
