package main

import (
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
)

// channelProvidable is the ONE place the gateway decides what value the channels
// package receives as its agent loop. pkg/channels resolves several OPTIONAL
// capabilities from that value with runtime type assertions (mcpPathsSource,
// agentSkillsSource, customCommandProvider, harnessCommandInvalidator); anything
// the returned type does not forward is silently absent in the real binary even
// though every fake-backed test stays green. cmd/lele/wiring_test.go pins that
// contract, so this line cannot rot again.
func channelProvidable(al *agent.AgentLoop) channels.AgentProvidable {
	return al.GetProvidable()
}
