// Production-wiring harness for the gateway MCP regression test
// (gateway_wiring_test.go, package channels_test).
//
// This is an EXPORT TEST FILE (package channels) because the test itself has
// to build a REAL *agent.AgentLoop — and pkg/agent imports pkg/channels, so an
// internal test file importing pkg/agent dies with "import cycle not allowed
// in test" — while the wiring assertions below need two unexported internals:
// NativeChannel.agentLoop (the value rest_mcp.go's runtime seam asserts
// against) and the AuthManager that owns the token the routes require. The
// external test package sees exported identifiers declared here, which is the
// standard export_test.go pattern.

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

// GatewayWiringFixture is a native channel wired exactly like the real
// gateway's, serving its routes on a test server with a paired bearer token.
type GatewayWiringFixture struct {
	Channel *NativeChannel
	Server  *httptest.Server
	Token   string
}

// NewGatewayWiringFixture mirrors cmd/lele/gateway.go: the channel manager is
// built from agentLoop.GetProvidable() — the agentProvidableImpl facade, not
// the *agent.AgentLoop — so every capability the MCP routes discover through a
// runtime assertion on NativeChannel.agentLoop has to be reachable on the
// facade. Anything the facade does not forward is silently absent in the real
// binary even though every fake-backed test stays green.
//
// It also asserts the seam up front (the brief's regression check): if the
// facade stops forwarding MCPPathsFor, the fixture fails naming the facade
// instead of leaving the failure to be discovered as an unexplained 500.
func NewGatewayWiringFixture(t *testing.T, cfg *config.Config, msgBus *bus.MessageBus, prov AgentProvidable) *GatewayWiringFixture {
	t.Helper()

	// The exact construction of gateway.go (channels.NewManager(cfg, msgBus,
	// agentLoop.GetProvidable(), approvalManager)).
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

	// The seams pkg/channels resolves per request with runtime assertions on
	// the value the gateway handed NewManager (the facade, not the loop). Each
	// entry names the assertion site and what silently breaks in the real
	// binary if the facade stops forwarding that capability — asserting all
	// four here means the fixture fails naming the facade instead of leaving
	// the failure to be discovered as an unexplained 500 or a stale registry.
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

// Get issues an authenticated GET against the fixture server and returns the
// status code with the raw body.
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
