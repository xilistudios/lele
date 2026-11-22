package main

import (
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
)

func channelProvidable(al *agent.AgentLoop) channels.AgentProvidable {
	return al.GetProvidable()
}
