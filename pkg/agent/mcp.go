// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

// mcpManager is the subset of *mcp.Manager pkg/agent depends on: server
// discovery, lazy tool loading, the RemoteTool factory and shutdown. The
// production implementation is *mcp.Manager; the interface exists so tests
// can inject close spies without touching pkg/mcp.
//
// Deliberately NOT part of the interface: LoadedNames(). The mcp_* tools of a
// session live in that session's own scoped registry layer (see
// sessionToolSet), which is dropped as a whole when the manager is retired —
// so nothing has to enumerate names for unregistration, and the manager's
// loaded cache (mutable, cleared by a mid-flight config change) is never
// consulted for it.
type mcpManager interface {
	// Servers re-reads the mcp.json layers and returns the active servers
	// sorted by name (excludes disabled/invalid entries).
	Servers() []tools.MCPServerInfo
	// LoadServer fetches and caches one server's tool specs (idempotent).
	LoadServer(ctx context.Context, server string) ([]tools.LoadedMCPTool, error)
	// ToolFactory builds the concrete RemoteTool for a loaded spec, wired to
	// this manager (the pkg/tools ↔ pkg/mcp seam of load_mcp_tools).
	ToolFactory() func(tools.LoadedMCPTool) tools.Tool
	// Close terminates every open connection (stdio children) and rejects
	// further dials; idempotent.
	Close() error
}

// mcpManagerEntry pairs a live manager with the layer paths it was built for.
// paths is kept so a reload that moved the agent's workspace (or the global
// config dir) retires the manager instead of letting it watch stale layers.
//
// The entry does NOT bookkeep the mcp_* names its loader registered: those live
// in per-session layers (see sessionToolSet), so retiring the manager drops
// every session's loaded tools at once — no per-name unregistration to get
// wrong.
type mcpManagerEntry struct {
	mgr   mcpManager
	paths mcp.Paths
}

// mcpManagerSet owns every per-agent MCP manager, keyed by agent ID (the
// same ownership model as the subagent and background-process maps). It is
// safe for concurrent use: sync passes (startup, config reload) run under
// syncMu while shutdown may close the set at any time.
type mcpManagerSet struct {
	// syncMu serialises whole sync passes so two concurrent reloads cannot
	// both create a manager for the same agent (the loser would be orphaned
	// with its connections open). closeAll takes it too, so "stopped" flips
	// only between whole sync passes — never in the middle of one.
	syncMu  sync.Mutex
	mu      sync.Mutex
	entries map[string]mcpManagerEntry
	// stopped is set by closeAll (teardown). Afterwards no manager may be
	// installed: syncMCPTools early-returns and put refuses the entry. It
	// exists because the config watcher's Stop does NOT join its goroutine
	// (pkg/config/watcher.go), so a reload already in flight can reach
	// syncMCPTools after services-stop — the flag guarantees such a late
	// sync cannot install a manager nobody closes.
	stopped bool
	// cwd is the process working directory captured ONCE at startup; it feeds
	// the project layer (<cwd>/.lele/mcp.json). Empty means os.Getwd failed,
	// which disables the layer (see mcp.Paths).
	cwd string
	// newManager builds the concrete manager; tests replace it to wrap the
	// result in a close spy. Read under syncMu (sync passes) — never written
	// after the wiring step.
	newManager func(mcp.Paths) mcpManager
	// layers owns the per-session scoped registries (session × agent) where
	// load_mcp_tools registers remote tools. They are created on demand and
	// dropped with their agent's manager: a layer's loader closes over that
	// manager, so it must not outlive it.
	layers *sessionToolSet
}

// newMCPManagerSet captures the process working directory at startup and the
// default manager factory (mcp.NewManager with a nil dialer ⇒ real dialer).
func newMCPManagerSet() *mcpManagerSet {
	cwd, err := os.Getwd()
	if err != nil {
		// No project layer — the agent and global layers still work.
		cwd = ""
	}
	return &mcpManagerSet{
		entries: make(map[string]mcpManagerEntry),
		layers:  newSessionToolSet(),
		cwd:     cwd,
		newManager: func(paths mcp.Paths) mcpManager {
			return mcp.NewManager(paths, nil)
		},
	}
}

// pathsFor builds the three-layer roots for one agent: global config dir,
// the agent's own workspace and the startup cwd (process-wide, see the plan:
// the project layer is identical for every agent of this process).
func (s *mcpManagerSet) pathsFor(agent *AgentInstance) mcp.Paths {
	return mcp.Paths{
		LeleDir:        config.GetLeleDir(),
		AgentWorkspace: agent.Workspace,
		Cwd:            s.cwd,
	}
}

// get returns the live entry for agentID, if any.
func (s *mcpManagerSet) get(agentID string) (mcpManagerEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[agentID]
	return entry, ok
}

// put stores entry for agentID, replacing (and closing) any previous manager
// so a replacement can never leave the old one's children running. The sync
// paths drop a retired entry before putting a new one (closeAndDrop), so the
// replace-and-close below only fires for a genuine replace by a future caller.
// After closeAll the set refuses new entries: the incoming manager is closed
// on the spot (belt — syncMCPTools already early-returns when stopped).
func (s *mcpManagerSet) put(agentID string, entry mcpManagerEntry) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		closeManagerQuietly(agentID, entry.mgr)
		return
	}
	previous, had := s.entries[agentID]
	s.entries[agentID] = entry
	s.mu.Unlock()
	if had && previous.mgr != entry.mgr {
		// A genuine replace: the layers built on the old manager must go with
		// it (their loader and remote tools are bound to it).
		s.layers.dropAgent(agentID)
		closeManagerQuietly(agentID, previous.mgr)
	}
}

// closeAndDrop closes agentID's manager and removes it from the set, together
// with every session layer built on it (those layers hold tools bound to this
// manager, so they go with it). It is a no-op when the agent has no manager.
// The close happens outside the lock: terminating a stdio child can block.
func (s *mcpManagerSet) closeAndDrop(agentID string) error {
	s.mu.Lock()
	entry, ok := s.entries[agentID]
	delete(s.entries, agentID)
	s.mu.Unlock()
	s.layers.dropAgent(agentID)
	if !ok {
		return nil
	}
	return entry.mgr.Close()
}

// dropSessionLayers removes every session layer of one agent: the sessions keep
// their manager (the servers are still configured) but lose the loader and the
// remote tools they had loaded through it. Used when the allowlist stops
// allowing load_mcp_tools — the loader is the only sanctioned way to create
// those tools, so keeping them would outlive the policy.
func (s *mcpManagerSet) dropSessionLayers(agentID string) {
	s.layers.dropAgent(agentID)
}

// sessionTools returns the tool registry a session must use: the agent's base
// registry, or — while the agent has a live manager — a session-scoped layer
// over it where load_mcp_tools registers the remote tools it loads.
//
// This is THE isolation point for MCP: the base registry is shared by every
// chat of the agent, so a load that reached it appeared in all of them. With a
// per-session layer a load is visible to exactly the session that asked for it,
// and the layer dies with the session (reset) or with the manager (retirement).
func (s *mcpManagerSet) sessionTools(agent *AgentInstance, sessionKey string) *tools.ToolRegistry {
	if agent == nil || agent.Tools == nil {
		return nil
	}
	// No session identity (startup probes, direct callers): the base registry
	// is the only meaningful answer — there is no layer to attribute a load to.
	if sessionKey == "" {
		return agent.Tools
	}
	entry, ok := s.get(agent.ID)
	if !ok || entry.mgr == nil {
		return agent.Tools
	}
	return s.layers.getOrCreate(agent.ID, sessionKey, func() *tools.ToolRegistry {
		layer := tools.NewScopedRegistry(agent.Tools)
		factory := entry.mgr.ToolFactory()
		layer.Register(tools.NewLoadMCPToolsTool(entry.mgr, layer, factory))
		return layer
	})
}

// closeExcept closes and drops every manager whose agent is not in
// liveAgentIDs — the reload path's "the agent disappeared" rule — and drops the
// session layers built on those managers. Errors are joined; the set is emptied
// either way. ReloadAgents deletes the whole AgentInstance from the registry,
// so its base tool registry dies with it; the layers are dropped here because
// they are keyed by agent ID and would otherwise outlive their manager.
func (s *mcpManagerSet) closeExcept(liveAgentIDs []string) error {
	live := make(map[string]bool, len(liveAgentIDs))
	for _, id := range liveAgentIDs {
		live[id] = true
	}

	s.mu.Lock()
	removed := make(map[string]mcpManagerEntry)
	for id, entry := range s.entries {
		if !live[id] {
			removed[id] = entry
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()

	var errs []error
	for id, entry := range removed {
		s.layers.dropAgent(id)
		if err := entry.mgr.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// closeAll closes every live manager exactly once, marks the set as stopped
// (no manager may be installed afterwards — see mcpManagerSet.stopped) and
// empties it: a second call finds nothing to close, which is what makes the
// shutdown hook idempotent. Safe when empty. It takes syncMu first (then mu),
// so it cannot flip stopped in the middle of a sync pass.
func (s *mcpManagerSet) closeAll() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	s.mu.Lock()
	s.stopped = true
	entries := s.entries
	s.entries = make(map[string]mcpManagerEntry)
	s.mu.Unlock()

	// Session layers hold tools bound to the managers being closed: drop them
	// before (or with) the close so no chat can call a dead server afterwards.
	s.layers.dropAll()

	var errs []error
	for id, entry := range entries {
		if err := entry.mgr.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp manager %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// isStopped reports whether closeAll already ran (read under mu; callers
// decide whether to hold syncMu as well).
func (s *mcpManagerSet) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// closeManagerQuietly closes a retired manager, logging (never surfacing)
// the error: it runs on the sync path, where a broken child termination must
// not abort the rest of the reload.
func closeManagerQuietly(agentID string, mgr mcpManager) {
	if mgr == nil {
		return
	}
	if err := mgr.Close(); err != nil {
		logger.WarnCF("agent", "Failed to close retired MCP manager",
			map[string]interface{}{"agent_id": agentID, "error": err.Error()})
	}
}

// syncMCPTools re-evaluates every live agent's MCP server set and mirrors the
// outcome into the agent's prompt and session layers. It runs once at startup
// (NewAgentLoop) and again on every config reload, right after
// updateSharedTools — the same cadence as the other feature-tool syncs.
//
// Per agent it: creates a manager only while at least one active server is
// configured, drops the session layers when the allowlist stops allowing
// load_mcp_tools, feeds the "## MCP Servers" prompt section through
// ContextBuilder.SetMCPServers, and closes + drops a manager whose server list
// became empty (which takes its session layers with it). Managers of agents
// removed from the config are closed afterwards.
//
// Design note: layers are built per session (see mcpManagerSet.sessionTools),
// and a subagent receives a flattened clone of its parent's BASE registry (see
// registerSharedToolsForAgent), so subagents do not inherit load_mcp_tools nor
// anything a session loaded in v1 — loading MCP tools stays a decision of the
// session itself.
func (tc *toolCoordinatorImpl) syncMCPTools() {
	if tc.al == nil || tc.registry == nil || tc.mcpManagers == nil {
		return
	}
	tc.mcpManagers.syncMu.Lock()
	defer tc.mcpManagers.syncMu.Unlock()

	// Teardown already ran (closeAll): no manager may be installed from here
	// on. The config watcher's Stop does not join its goroutine, so a reload
	// already in flight can legitimately reach this point after services-stop;
	// the stopped flag turns such a late sync into a no-op instead of leaving
	// an unowned manager (and its stdio children) behind.
	if tc.mcpManagers.isStopped() {
		return
	}

	cfg := tc.al.cfg()
	live := tc.registry.ListAgentIDs()
	for _, agentID := range live {
		agent, ok := tc.registry.GetAgent(agentID)
		if !ok || agent == nil {
			continue
		}
		tc.syncMCPServersForAgent(cfg, agent, agentID)
	}
	if err := tc.mcpManagers.closeExcept(live); err != nil {
		logger.WarnCF("agent", "Failed to close MCP managers of removed agents",
			map[string]interface{}{"error": err.Error()})
	}
}

// syncMCPServersForAgent applies the MCP wiring to one agent: manager
// lifecycle first, then the prompt section.
//
// The sync no longer touches the agent's tool registry: load_mcp_tools lives in
// each SESSION's scoped layer (see mcpManagerSet.sessionTools), so there is
// nothing to register here and nothing to unregister when the configuration
// changes — retiring the manager drops its session layers as a whole.
//
// The allowlist check mirrors syncGroupChatTool: on the reload path
// updateSharedToolsForAgent early-returns for preserved agents (no allowlist
// pass runs), so the gate has to live in the sync itself or a reload would let
// a restricted agent keep loading MCP tools.
func (tc *toolCoordinatorImpl) syncMCPServersForAgent(cfg *config.Config, agent *AgentInstance, agentID string) {
	set := tc.mcpManagers
	if agent == nil || agent.Tools == nil {
		return
	}
	paths := set.pathsFor(agent)

	entry, hasMgr := set.get(agentID)
	if hasMgr && entry.paths != paths {
		// The workspace (or the global dir) moved: the manager watches stale
		// mcp.json layers, so retire it — close AND drop it, so a later move
		// back creates a fresh manager instead of reusing this closed one.
		// Its session layers go with it (closeAndDrop drops them).
		if err := set.closeAndDrop(agentID); err != nil {
			logger.WarnCF("agent", "Failed to close MCP manager after paths changed",
				map[string]interface{}{"agent_id": agentID, "error": err.Error()})
		}
		entry, hasMgr = mcpManagerEntry{}, false
	}

	var servers []tools.MCPServerInfo
	if hasMgr {
		servers = entry.mgr.Servers()
		if len(servers) == 0 {
			// Reload removed every server: close the children, drop the
			// manager and its session layers (otherwise stale remote tools
			// would linger in those sessions with no manager behind them).
			if err := set.closeAndDrop(agentID); err != nil {
				logger.WarnCF("agent", "Failed to close MCP manager with no servers left",
					map[string]interface{}{"agent_id": agentID, "error": err.Error()})
			}
			tc.setMCPServers(agent, nil)
			return
		}
	} else {
		mgr := set.newManager(paths)
		servers = mgr.Servers()
		if len(servers) == 0 {
			// No server anywhere: close the fresh manager (nothing was ever
			// dialed, so this is bookkeeping only) and keep it out of the
			// set — a manager exists iff it has active servers.
			closeManagerQuietly(agentID, mgr)
			tc.setMCPServers(agent, nil)
			return
		}
		set.put(agentID, mcpManagerEntry{mgr: mgr, paths: paths})
	}

	// Servers are configuration, not tools: the prompt section is fed
	// regardless of the allowlist below, so the user still sees what is
	// configured even when load_mcp_tools itself was filtered out.
	tc.setMCPServers(agent, servers)

	if !allowlistKeeps(agentConfigForID(cfg, agentID), tools.LoadMCPToolsName) {
		// The allowlist tightened: the session layers go (they hold the loader
		// and the mcp_* tools already loaded through it). The manager stays —
		// the servers are still configured, they are just not loadable by this
		// agent any more.
		set.dropSessionLayers(agentID)
	}
}

// sessionTools returns the tool registry a session must use: the agent's base
// registry, or — while MCP servers are configured for that agent and the
// allowlist keeps load_mcp_tools — a session-scoped layer over it.
//
// This is what keeps MCP loads session-local. Every place that builds tool
// definitions or executes a tool call for a session must go through it (see
// llmRunner.runAgentLoop, runGroupTurn and toolExecutor): the loader the model
// calls writes into the layer, so the remote tools it registers are offered to
// that session only, never to the other chats served by the same agent.
func (tc *toolCoordinatorImpl) sessionTools(agent *AgentInstance, sessionKey string) *tools.ToolRegistry {
	if agent == nil || agent.Tools == nil {
		return nil
	}
	if tc.mcpManagers == nil {
		return agent.Tools
	}
	// Belt for the reload window: a tightened allowlist drops the layers during
	// the sync, so a layer must never be (re)built for an agent that no longer
	// allows the loader.
	var cfg *config.Config
	if tc.al != nil {
		cfg = tc.al.cfg()
	}
	if !allowlistKeeps(agentConfigForID(cfg, agent.ID), tools.LoadMCPToolsName) {
		return agent.Tools
	}
	return tc.mcpManagers.sessionTools(agent, sessionKey)
}

// dropSessionTools forgets the scoped tool layers of one session: its loaded
// MCP tools go with the history it reset. No-op when MCP is not configured.
func (tc *toolCoordinatorImpl) dropSessionTools(sessionKey string) {
	if tc.mcpManagers == nil || sessionKey == "" {
		return
	}
	tc.mcpManagers.layers.dropSession(sessionKey)
}

// setMCPServers forwards the resolved server list to the agent's prompt
// builder (T9: the builder invalidates the cached static prompt only when
// the name+description+layer set actually changed — see mcpServersEqual).
func (tc *toolCoordinatorImpl) setMCPServers(agent *AgentInstance, servers []tools.MCPServerInfo) {
	if agent.ContextBuilder == nil {
		return
	}
	agent.ContextBuilder.SetMCPServers(servers)
}

// closeMCPManagers closes every live per-agent MCP manager exactly once
// (idempotent and nil-safe). It is AgentLoop's teardown entry point, called
// from Shutdown after a clean drain and from the gateway's mcp-stop hook.
func (tc *toolCoordinatorImpl) closeMCPManagers() error {
	if tc.mcpManagers == nil {
		return nil
	}
	return tc.mcpManagers.closeAll()
}

// CloseMCPManagers closes every live per-agent MCP manager, terminating the
// stdio children spawned for configured MCP servers. Safe to call twice (the
// second call is a no-op) and safe when no manager exists. Exposed for the
// gateway's "mcp-stop" shutdown hook.
func (al *AgentLoop) CloseMCPManagers() error {
	if al.toolCoordinator == nil {
		return nil
	}
	return al.toolCoordinator.closeMCPManagers()
}

// SyncMCPServers re-runs the MCP wiring pass for EVERY live agent: each
// agent's mcp.json layers are re-read and the outcome mirrored into that
// agent's tool registry (load_mcp_tools) and its "## MCP Servers" prompt
// section.
//
// Why this exists: mcp.json is NOT watched by the config watcher (only
// config.json is), and while the data plane re-reads the mcp.json layers
// lazily on every remote call, the tool/prompt SURFACE — load_mcp_tools
// registration and the "## MCP Servers" prompt section — only updates on
// this pass or on ReloadRegistry. Without this call an out-of-band mcp.json
// edit (WebUI MCP page, raw editor, TUI toggle) stays invisible until the
// next config reload.
//
// Do NOT "simplify" this into a config reload (reloadConfig →
// ReloadRegistry): the blast radius is different in kind — ReloadRegistry
// recreates ContextBuilders, cancels subagents of removed agents and swaps
// the whole tool registry, and it would fail the caller's already-landed
// mcp.json write whenever an unrelated config.json happens to be broken.
// The two concerns have no causal link; this pass is the whole scope.
//
// Contract: it re-runs toolCoordinator.syncMCPTools() — the same pass
// ReloadRegistry ends with — so it is nil-safe on a bare &AgentLoop{} (the
// read-and-nil-check shape of CloseMCPManagers above), concurrency-safe by
// reusing mcpManagers.syncMu and the existing `stopped` no-op (no new
// mutex, no re-implemented guard), and it returns nothing: a wiring problem
// is logged, never reported, because the caller's write already succeeded
// (the channel callback is func() for exactly that reason).
func (al *AgentLoop) SyncMCPServers() {
	if al.toolCoordinator == nil {
		return
	}
	al.toolCoordinator.syncMCPTools()
}

// MCPPathsFor returns the three mcp.json roots one agent reads — exactly what
// that agent's MCP manager resolved: mcpManagerSet.pathsFor(agent), i.e.
// LeleDir=config.GetLeleDir(), AgentWorkspace=agent.Workspace and Cwd=the
// startup cwd captured by the set. It must NOT re-derive the roots from
// config.json or from pkg/channels' agentWorkspaceDir: the two resolvers
// disagreeing is the bug this seam prevents (plan EVIDENCE B2).
//
// ok=false means the agent is not in the live registry (or the loop is bare
// or its coordinator is not the concrete impl); the zero Paths must not be
// used in that case.
func (al *AgentLoop) MCPPathsFor(agentID string) (mcp.Paths, bool) {
	if al.toolCoordinator == nil || al.registry == nil {
		return mcp.Paths{}, false
	}
	tc, ok := al.toolCoordinator.(*toolCoordinatorImpl)
	if !ok || tc.mcpManagers == nil {
		return mcp.Paths{}, false
	}
	agent, ok := al.registry.GetAgent(agentID)
	if !ok || agent == nil {
		return mcp.Paths{}, false
	}
	return tc.mcpManagers.pathsFor(agent), true
}
