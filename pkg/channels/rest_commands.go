// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"net/http"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
)

// ChatCommandsResponse is the payload of GET /api/v1/chat/commands: the slash
// commands the backend dispatches and the WebUI may advertise in its palette.
//
// CommandInfo comes from pkg/agent/commands, the registry package, not from
// pkg/agent itself: pkg/agent depends on pkg/channels (AgentProvidable), so
// importing it here would be an import cycle. pkg/agent re-exports the very
// same type (agent.CommandInfo is an alias), so both entry points are identical
// on the wire and in Go.
type ChatCommandsResponse struct {
	Commands []agentcommands.CommandInfo `json:"commands"`
}

// handleChatCommands lists the backend slash commands for UI clients.
//
// GET /api/v1/chat/commands -> {"commands":[{"name","description","usage"},...]}
//
// The list is read straight from the command registry, which is package data
// rather than per-session state, so this handler never touches n.agentLoop and
// cannot fail for a missing agent loop. It is also session-independent: there is
// no session key to validate ownership of, so withAuth at registration is the
// only gate.
//
// The registry is already sorted and copied by WebUICommands(), so it is safe to
// hand the slice straight to the encoder.
func (n *NativeChannel) handleChatCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	writeJSON(w, http.StatusOK, ChatCommandsResponse{
		Commands: agentcommands.WebUICommands(),
	})
}
