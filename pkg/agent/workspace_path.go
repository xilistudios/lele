// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"sort"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/routing"
)

// This file exposes the registry's agent → workspace resolution as PURE
// lookups (no instance built, nothing written to disk), for callers that need
// to know where an agent's layer files live WITHOUT starting the runtime:
// `lele mcp` resolves the agent layer root from config alone. The registry
// cannot be used for that — NewAgentRegistry builds every instance through
// NewAgentInstance, which initializes the workspace on disk (InitializeWorkspace
// creates the directory, its `memory/` subdirectory and the context files), so
// a read-only `lele mcp list` used to create `<cwd>/memory` as a side effect.
//
// Both functions mirror NewAgentRegistry's ID rule exactly — an empty
// agents.list means the single implicit "main" agent, otherwise every
// configured agent is keyed by routing.NormalizeAgentID with the LAST
// occurrence winning (the registry's map-assignment order) — and delegate the
// workspace rule itself to resolveAgentWorkspace, so each rule keeps exactly
// one implementation and the CLI can never drift from the runtime.

// AgentWorkspacePath returns the workspace directory the registry would
// assign to agentID, and whether an agent with that ID exists at all. It is
// pure: no AgentInstance is created and nothing is written to disk (contrast
// NewAgentRegistry + GetAgent, which initialize the workspace).
//
// ok=false means "unknown agent": the caller must not use the (empty) path.
// The rule for an ID that normalizes to nothing is routing.NormalizeAgentID's
// own — it yields "main", so an all-punctuation ID addresses the default
// agent exactly as the registry routes it.
func AgentWorkspacePath(cfg *config.Config, agentID string) (string, bool) {
	id := routing.NormalizeAgentID(agentID)
	if len(cfg.Agents.List) == 0 {
		if id != "main" {
			return "", false
		}
		// The implicit agent NewAgentRegistry builds: Default=true, so
		// resolveAgentWorkspace hands back agents.defaults.workspace.
		implicit := &config.AgentConfig{ID: "main", Default: true}
		return resolveAgentWorkspace(implicit, &cfg.Agents.Defaults), true
	}
	// Duplicate IDs collapse in the registry's map (last occurrence wins);
	// keep scanning so this lookup agrees with it.
	workspace, found := "", false
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if routing.NormalizeAgentID(ac.ID) != id {
			continue
		}
		workspace, found = resolveAgentWorkspace(ac, &cfg.Agents.Defaults), true
	}
	return workspace, found
}

// AgentIDs returns the normalized IDs of every agent a registry built from
// cfg would hold, sorted and deduplicated — the same key set as
// AgentRegistry.ListAgentIDs, without building a single instance. The
// implicit "main" agent is returned alone when agents.list is empty.
func AgentIDs(cfg *config.Config) []string {
	if len(cfg.Agents.List) == 0 {
		return []string{"main"}
	}
	seen := make(map[string]bool, len(cfg.Agents.List))
	ids := make([]string, 0, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		id := routing.NormalizeAgentID(cfg.Agents.List[i].ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
