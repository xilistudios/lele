// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

// Package commands holds the registry of backend-dispatched slash commands that
// UI clients advertise (the WebUI palette today).
//
// It is a leaf package on purpose: pkg/agent depends on pkg/channels (see
// channels.AgentProvidable), so pkg/channels cannot import pkg/agent without an
// import cycle. The registry therefore lives below pkg/agent, where both sides
// can reach it — the same shape the repo already uses for data shared between
// those two packages (pkg/context.ContextFiles, pkg/tui/i18n).
//
// pkg/agent re-exports this registry as agent.CommandInfo / agent.WebUICommands,
// which is the canonical entry point; the re-export is guarded by
// TestCommandRegistry_ReexportMatchesSource in pkg/agent.
package commands

import (
	"sort"
	"strings"
)

// CommandInfo describes a backend-dispatched slash command for UI clients.
type CommandInfo struct {
	Name        string `json:"name"`        // e.g. "/clear"
	Description string `json:"description"` // short human-readable description (English)
	Usage       string `json:"usage"`       // e.g. "/clear" or "/compact"
	// Source is where the definition came from: empty for built-ins (the
	// registry above) and one of the harness discovery levels ("config",
	// "global", "workspace", "directory") for user-defined commands. It is
	// omitempty so built-ins stay byte-identical on the wire and UIs can tell
	// the two apart without a name allowlist.
	Source string `json:"source,omitempty"` // "" = built-in; else harness.Source
}

// webUICommands is the source of truth for the commands UI-visible clients may
// show. It is the extension point for future custom commands: registering an
// entry here is what makes a command appear in the palette, with no per-client
// changes.
//
// This list is deliberately NOT wired into commandHandlerImpl.handleCommand.
// The switch stays the authority on what the backend *accepts*; this registry is
// the authority on what the backend *tells clients about*. Rewiring dispatch
// through the registry is out of scope (it is a hot path with many commands and
// tests behind it), so entries must be kept in sync with the switch by hand —
// TestWebUICommands_MatchDispatchedCommands in pkg/agent guards that drift.
//
// Only commands that are safe to run from the WebUI belong here: dispatched by
// handleCommand, session-scoped, and usable with no arguments.
var webUICommands = []CommandInfo{
	{
		Name:        "/clear",
		Description: "Clear the conversation history for this session.",
		Usage:       "/clear",
	},
	{
		Name:        "/compact",
		Description: "Summarize and compact the conversation history (needs 5+ messages).",
		Usage:       "/compact",
	},
}

// WebUICommands returns the slash commands UI clients should advertise.
//
// The result is always a fresh copy sorted by name, so callers can neither
// mutate the registry nor depend on the (unstable) declaration order.
func WebUICommands() []CommandInfo {
	out := make([]CommandInfo, len(webUICommands))
	copy(out, webUICommands)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CustomCommandInfo describes a user-defined (harness) slash command for UI
// clients. It is the harness counterpart of CommandInfo: Name/Description/Usage
// follow the same shape so clients can render both kinds uniformly, plus Source
// which tells where the definition came from ("config", "global", "workspace"
// or "directory") so UIs can badge custom entries.
type CustomCommandInfo struct {
	Name        string `json:"name"`        // e.g. "/review"
	Description string `json:"description"` // short human-readable description
	Usage       string `json:"usage"`       // e.g. "/review $ARGUMENTS"
	Source      string `json:"source"`      // discovery level (harness.Source)
}

// WithCustom merges custom commands into a base (built-in) command list for UI
// presentation. Built-ins win name collisions: a custom command shadowed by a
// dispatched built-in is dropped, because the backend would never reach it.
//
// Names are normalized to a leading slash and compared case-insensitively, so
// "/Review" and "review" collide with "/review". The result is always a fresh
// slice sorted by name; neither input is mutated.
func WithCustom(base []CommandInfo, custom []CustomCommandInfo) []CommandInfo {
	out := make([]CommandInfo, 0, len(base)+len(custom))
	seen := make(map[string]struct{}, len(base)+len(custom))

	for _, c := range base {
		out = append(out, c)
		seen[normalizeCommandName(c.Name)] = struct{}{}
	}

	for _, c := range custom {
		key := normalizeCommandName(c.Name)
		if key == "" || key == "/" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, CommandInfo{
			Name:        key,
			Description: c.Description,
			Usage:       c.Usage,
			Source:      c.Source,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// normalizeCommandName makes a command name comparable and presentable:
// lowercase, with exactly one leading slash.
func normalizeCommandName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimLeft(n, "/")
	if n == "" {
		return ""
	}
	return "/" + n
}
