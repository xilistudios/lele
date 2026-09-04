// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"github.com/xilistudios/lele/pkg/agent/commands"
)

// CommandInfo describes a backend-dispatched slash command for UI clients.
//
// It is an alias of commands.CommandInfo, which is where the registry actually
// lives. The registry has to sit in a leaf package because pkg/agent depends on
// pkg/channels (channels.AgentProvidable), so pkg/channels cannot import
// pkg/agent — and the WebUI command endpoint needs the registry from
// pkg/channels. Aliasing keeps agent.CommandInfo the canonical name callers
// outside the channels package should use while guaranteeing both packages see
// the very same type and the very same list.
type CommandInfo = commands.CommandInfo

// WebUICommands returns the slash commands UI clients should advertise.
//
// Delegates to commands.WebUICommands, the single source of truth, which always
// hands back a fresh copy sorted by name. See that function for the rules a
// command must satisfy to be registered.
func WebUICommands() []CommandInfo {
	return commands.WebUICommands()
}
