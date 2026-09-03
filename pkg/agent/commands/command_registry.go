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

import "sort"

// CommandInfo describes a backend-dispatched slash command for UI clients.
type CommandInfo struct {
	Name        string `json:"name"`        // e.g. "/clear"
	Description string `json:"description"` // short human-readable description (English)
	Usage       string `json:"usage"`       // e.g. "/clear" or "/compact"
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
