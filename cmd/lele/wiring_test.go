package main

import (
	"testing"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/skills"
)

// Structural mirrors of the four OPTIONAL capabilities pkg/channels resolves
// with runtime type assertions on the value gateway.go hands NewManager. They
// are unexported in pkg/channels (rest_mcp.go:76, rest_agent_skills.go:68,
// rest_commands.go:38, rest_agent_commands.go:847) and pkg/channels can never
// import pkg/agent to name the real ones — pkg/agent imports pkg/channels at
// agent_providable.go:16, so that is a real import cycle. Declaring the same
// method sets here keeps the check honest without breaking the cycle.
type mcpPathsSeam interface {
	MCPPathsFor(agentID string) (mcp.Paths, bool)
}

type skillsSeam interface {
	AgentSkills(agentID string) (*skills.SkillsLoader, func(), bool)
}

type commandsSeam interface {
	HarnessCommands() []*harness.Command
}

type invalidatorSeam interface {
	InvalidateHarnessWorkspace(workspace string)
}

// TestChannelProvidable_SatisfiesChannelsSeams pins the contract of the ONE
// expression the gateway uses to build the channels package's agent loop: the
// value channelProvidable returns must satisfy every optional capability
// pkg/channels asserts at runtime, or the real binary silently degrades (or
// 500s) while fake-backed tests stay green.
func TestChannelProvidable_SatisfiesChannelsSeams(t *testing.T) {
	// Same construction as pkg/channels/gateway_wiring_test.go: the gateway's
	// constructor with a temp config; store nil is the documented JSON fallback
	// and irrelevant to the method set under test.
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.Model = "test-model"
	msgBus := bus.NewMessageBus()
	loop := agent.NewAgentLoopWithStore(cfg, msgBus, nil)
	t.Cleanup(func() { loop.Stop() })

	prov := channelProvidable(loop)

	for _, seam := range []struct {
		name   string
		site   string // where pkg/channels asserts this capability
		checks func(channels.AgentProvidable) bool
		breaks string // what silently breaks in production when absent
	}{
		{
			name:   "mcpPathsSource",
			site:   "pkg/channels/rest_mcp.go:91 (interface at rest_mcp.go:76)",
			checks: func(p channels.AgentProvidable) bool { _, ok := p.(mcpPathsSeam); return ok },
			breaks: "GET/POST /api/v1/mcp* answer 500 mcp_unavailable for every agent",
		},
		{
			name:   "agentSkillsSource",
			site:   "pkg/channels/rest_agent_skills.go:82 (interface at rest_agent_skills.go:68)",
			checks: func(p channels.AgentProvidable) bool { _, ok := p.(skillsSeam); return ok },
			breaks: "per-agent skills endpoints silently fall back to the channel-wide default loader, so agent X reads the default agent's skills",
		},
		{
			name:   "customCommandProvider",
			site:   "pkg/channels/rest_commands.go:105 (interface at rest_commands.go:38)",
			checks: func(p channels.AgentProvidable) bool { _, ok := p.(commandsSeam); return ok },
			breaks: "the slash-command palette silently loses every user-defined harness command (nil, no error)",
		},
		{
			name:   "harnessCommandInvalidator",
			site:   "pkg/channels/rest_agent_commands.go:868 (interface at rest_agent_commands.go:847)",
			checks: func(p channels.AgentProvidable) bool { _, ok := p.(invalidatorSeam); return ok },
			breaks: "every REST command write (create/update/toggle) silently skips invalidation and the dispatcher serves a stale registry for up to harnessRefreshTTL (30s)",
		},
	} {
		if !seam.checks(prov) {
			t.Errorf("channelProvidable(loop) does not satisfy %s, asserted at %s: %s",
				seam.name, seam.site, seam.breaks)
		}
	}
}
