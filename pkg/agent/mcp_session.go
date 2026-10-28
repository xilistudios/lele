// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"sync"

	"github.com/xilistudios/lele/pkg/tools"
)

// maxSessionToolLayers caps how many session layers one agent may keep at the
// same time. A layer holds only the session's loaded MCP tools (a handful of
// pointers), so the cap is about never growing without bound on a long-lived
// gateway where sessions come and go; the oldest (least recently used) layers
// are dropped first. Dropping a layer only means its chat has to call
// load_mcp_tools again — the MCP connections live in the manager, not here.
const maxSessionToolLayers = 256

// sessionToolKey identifies one session layer: MCP tools are per agent
// instance and per session, so both dimensions are part of the key.
type sessionToolKey struct {
	agentID    string
	sessionKey string
}

// sessionToolLayer is one session's scoped tool registry plus the clock stamp
// used for LRU retirement.
type sessionToolLayer struct {
	registry *tools.ToolRegistry
	used     uint64
}

// sessionToolSet owns the session-scoped tool layers of every agent.
//
// WHY this exists: a tool registry belongs to an agent instance, and every chat
// served by that agent shares it. Registering MCP tools straight into it (the
// pre-fix behaviour) leaked them into every other session of the same agent —
// session B would see the remote tools session A loaded. A layer is a scoped
// registry over the agent's base registry, so a load is visible to exactly the
// session that performed it while the base (shared) registry stays untouched.
//
// Ownership: the set lives inside mcpManagerSet, so the layers of an agent are
// dropped together with that agent's MCP manager (retirement, teardown): a
// layer's load_mcp_tools closes over the manager it was built with and must
// never outlive it.
type sessionToolSet struct {
	mu      sync.Mutex
	entries map[sessionToolKey]*sessionToolLayer
	clock   uint64
}

// newSessionToolSet builds an empty, ready-to-use set.
func newSessionToolSet() *sessionToolSet {
	return &sessionToolSet{entries: make(map[sessionToolKey]*sessionToolLayer)}
}

// getOrCreate returns the layer for (agentID, sessionKey), building it once
// with build(). build runs UNDER the set lock, which is what makes "one layer
// per session" atomic: two concurrent turns of the same session must share the
// same layer or the second one would not see what the first loaded. build only
// allocates a registry and a closure, so holding the lock across it is cheap
// and cannot re-enter the set.
func (s *sessionToolSet) getOrCreate(agentID, sessionKey string, build func() *tools.ToolRegistry) *tools.ToolRegistry {
	key := sessionToolKey{agentID: agentID, sessionKey: sessionKey}

	s.mu.Lock()
	defer s.mu.Unlock()
	if layer, ok := s.entries[key]; ok {
		s.clock++
		layer.used = s.clock
		return layer.registry
	}
	registry := build()
	if registry == nil {
		return nil
	}
	s.clock++
	s.entries[key] = &sessionToolLayer{registry: registry, used: s.clock}
	s.pruneLocked()
	return registry
}

// pruneLocked drops the least recently used layers while the set is over the
// cap. Callers hold s.mu.
func (s *sessionToolSet) pruneLocked() {
	for len(s.entries) > maxSessionToolLayers {
		var (
			oldestKey sessionToolKey
			oldest    uint64
			found     bool
		)
		for key, layer := range s.entries {
			if !found || layer.used < oldest {
				oldestKey, oldest, found = key, layer.used, true
			}
		}
		if !found {
			return
		}
		delete(s.entries, oldestKey)
	}
}

// dropAgent removes every layer of one agent. Called when the agent's MCP
// manager is retired or closed: the layers hold tools bound to that manager.
func (s *sessionToolSet) dropAgent(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.agentID == agentID {
			delete(s.entries, key)
		}
	}
}

// dropSession removes every layer of one session, across agents (a session can
// switch agents): the session's history and overrides were cleared, so the MCP
// tools it loaded must go too.
func (s *sessionToolSet) dropSession(sessionKey string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.sessionKey == sessionKey {
			delete(s.entries, key)
		}
	}
}

// dropAll removes every layer (process teardown).
func (s *sessionToolSet) dropAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[sessionToolKey]*sessionToolLayer)
}

// len returns the number of live layers (tests and diagnostics).
func (s *sessionToolSet) len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
