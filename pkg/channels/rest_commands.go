// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"net/http"
	"strings"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/harness"
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

// customCommandProvider is the OPTIONAL capability an agent loop may implement
// to advertise user-defined (harness) slash commands. It is asserted against
// n.agentLoop, never declared on AgentProvidable: adding a method to that
// interface would break every fake and mock implementing it (and every other
// channel), while an assertion degrades gracefully when the loop does not
// support custom commands — the palette then just shows the built-ins.
//
// *agent.Loop satisfies it via HarnessCommands(); the signature is kept
// structurally identical so the real loop matches without importing pkg/agent.
type customCommandProvider interface {
	HarnessCommands() []*harness.Command
}

// handleChatCommands lists the backend slash commands for UI clients: the
// dispatched built-ins merged with the harness custom commands the agent loop
// knows about, built-ins winning name collisions.
//
// GET /api/v1/chat/commands ->
//
//	{"commands":[{"name","description","usage","source"?},...]}
//
// The built-in part is read straight from the command registry, which is package
// data rather than per-session state. The custom part is read from the loop via
// the optional customCommandProvider assertion above, so a missing loop, a loop
// that does not implement the capability, or a nil slice from it all simply
// yield the built-ins: this handler cannot fail for agent-loop reasons. It is
// also session-independent: there is no session key to validate ownership of, so
// withAuth at registration is the only gate.
//
// WithCustom already hands back a fresh copy sorted by name, so it is safe to
// hand the slice straight to the encoder.
func (n *NativeChannel) handleChatCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	writeJSON(w, http.StatusOK, ChatCommandsResponse{
		Commands: n.chatCommands(),
	})
}

// chatCommands builds the palette payload: built-ins plus the harness commands
// the agent loop currently resolves (all four discovery levels, precedence
// already applied by the harness registry).
func (n *NativeChannel) chatCommands() []agentcommands.CommandInfo {
	base := agentcommands.WebUICommands()

	// n.agentLoop is an interface: a nil *agent.Loop stored in it would be a
	// non-nil interface holding a nil pointer, so guard the interface itself
	// AND the assertion result before calling through it.
	if n.agentLoop == nil {
		return base
	}
	provider, ok := n.agentLoop.(customCommandProvider)
	if !ok || provider == nil {
		return base
	}

	custom := provider.HarnessCommands()
	if len(custom) == 0 {
		return base
	}
	return agentcommands.WithCustom(base, harnessCommandsAsCustom(custom))
}

// harnessCommandsAsCustom maps resolved harness commands onto the wire shape.
//
// Name keeps no leading slash on the harness side while the registry speaks
// slashed names, so it is prefixed here; WithCustom normalizes (lowercases,
// single slash) and drops collisions with built-ins, which is exactly the
// dispatch reality — the built-in switch always wins.
//
// Usage is synthesised as "/name [args]": a harness template may or may not take
// arguments ($ARGUMENTS), and the loader does not expose that distinction, but
// advertising "[args]" is harmless — it only tells the UI that trailing text is
// accepted, and an argumentless command still works with no arguments.
//
// The name is lowercased here because WithCustom normalizes Name but passes
// Usage through untouched: without this, "/Review" and "/Review [args]" could
// disagree in case on the wire.
func harnessCommandsAsCustom(cmds []*harness.Command) []agentcommands.CustomCommandInfo {
	out := make([]agentcommands.CustomCommandInfo, 0, len(cmds))
	for _, c := range cmds {
		if c == nil || c.Name == "" {
			continue
		}
		name := "/" + strings.ToLower(strings.TrimLeft(c.Name, "/"))
		out = append(out, agentcommands.CustomCommandInfo{
			Name:        name,
			Description: c.Description,
			Usage:       name + " [args]",
			Source:      string(c.Source),
		})
	}
	return out
}
