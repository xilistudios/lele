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
	"slices"
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
// Deliberately NOT part of the interface: LoadedNames(). Which mcp_* names
// belong to an agent's registry is bookkept by the ENTRY (see
// mcpManagerEntry.registered) — the manager's loaded cache is mutable state
// that a mid-flight config change clears, so it cannot be the source of
// truth for unregistration.
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

// nameSet is a mutex-guarded set of registry names: the mcp_* tool names a
// single mcpManagerEntry registered into its agent's ToolRegistry. It is the
// bookkeeping source for retirement (unregisterLoadedMCPTools), which must
// remove EXACTLY what this entry registered — never derived from the
// manager's loaded cache (a config edit clears that cache and would cause
// under-unregistration). Methods are nil-safe: entries injected directly by
// tests may carry no set.
type nameSet struct {
	mu    sync.Mutex
	names map[string]struct{}
}

// newNameSet builds an empty, ready-to-use set.
func newNameSet() *nameSet {
	return &nameSet{names: make(map[string]struct{})}
}

// add records one registered name.
func (s *nameSet) add(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.names[name] = struct{}{}
	s.mu.Unlock()
}

// snapshot returns the recorded names sorted (deterministic unregistration).
func (s *nameSet) snapshot() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.names))
	for name := range s.names {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// clear empties the set (after its names were unregistered).
func (s *nameSet) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.names = make(map[string]struct{})
	s.mu.Unlock()
}

// mcpManagerEntry pairs a live manager with the layer paths it was built for.
// paths is kept so a reload that moved the agent's workspace (or the global
// config dir) retires the manager instead of letting it watch stale layers.
// registered holds the names THIS entry's loader actually put into the
// agent's registry; every entry starts with an empty set (a replaced/rebuilt
// manager re-records from scratch).
type mcpManagerEntry struct {
	mgr        mcpManager
	paths      mcp.Paths
	registered *nameSet
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
		closeManagerQuietly(agentID, previous.mgr)
	}
}

// closeAndDrop closes agentID's manager and removes it from the set. It is a
// no-op when the agent has no manager. The close happens outside the lock:
// terminating a stdio child can block.
func (s *mcpManagerSet) closeAndDrop(agentID string) error {
	s.mu.Lock()
	entry, ok := s.entries[agentID]
	delete(s.entries, agentID)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return entry.mgr.Close()
}

// closeExcept closes and drops every manager whose agent is not in
// liveAgentIDs — the reload path's "the agent disappeared" rule. Errors are
// joined; the set is emptied either way. The agents' loaded mcp_* names need
// no unregistration here: ReloadAgents deletes the whole AgentInstance from
// the registry, so its tool registry dies with it.
func (s *mcpManagerSet) closeExcept(liveAgentIDs []string) error {
	live := make(map[string]bool, len(liveAgentIDs))
	for _, id := range liveAgentIDs {
		live[id] = true
	}

	s.mu.Lock()
	var removed []mcpManagerEntry
	for id, entry := range s.entries {
		if !live[id] {
			removed = append(removed, entry)
			delete(s.entries, id)
		}
	}
	s.mu.Unlock()

	var errs []error
	for _, entry := range removed {
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

// unregisterLoadedMCPTools removes every tool THIS entry registered into the
// agent's registry: it iterates entry.registered — the names the entry's own
// wrapped factory recorded (skip-if-present in LoadMCPToolsTool guarantees
// the factory runs exactly for the names this agent registered) — never the
// manager's loaded cache, which a mid-flight config change clears (that was
// the under-unregistration bug: names silently left behind with no manager
// behind them) and which can hold specs that were never registered at all
// (over-unregistration on a cross-server name collision). The set is cleared
// after unregistering, so a reused entry starts clean. Called wherever a
// manager is retired or the loader is taken away; the agent-removed case
// (closeExcept) needs no call: the registry deletes the whole AgentInstance,
// its tools die with it.
func unregisterLoadedMCPTools(agent *AgentInstance, entry mcpManagerEntry) {
	if agent == nil || agent.Tools == nil || entry.mgr == nil {
		return
	}
	for _, name := range entry.registered.snapshot() {
		agent.Tools.Unregister(name)
	}
	entry.registered.clear()
}

// syncMCPTools re-evaluates every live agent's MCP server set and mirrors the
// outcome into the agent's toolset and system prompt. It runs once at startup
// (NewAgentLoop) and again on every config reload, right after
// updateSharedTools — the same cadence as the other feature-tool syncs.
//
// Per agent it: creates a manager only while at least one active server is
// configured, registers load_mcp_tools only when that manager has servers
// (and the allowlist keeps it), unregisters it otherwise (together with the
// mcp_* tools already loaded through it), feeds the "## MCP Servers" prompt
// section through ContextBuilder.SetMCPServers, and closes + drops a manager
// whose server list became empty. Managers of agents removed from the config
// are closed afterwards.
//
// Design note: load_mcp_tools is registered after the subagent tool clone has
// been taken (see registerSharedToolsForAgent), so subagents do not inherit
// it in v1 — loading MCP tools stays a decision of the main agent.
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
// lifecycle first, then the prompt section, then the load_mcp_tools tool.
//
// The allowlist check mirrors syncGroupChatTool: on the reload path
// updateSharedToolsForAgent early-returns for preserved agents (no allowlist
// pass runs), so the gate has to live in the sync itself or a reload would
// hand load_mcp_tools back to an agent whose allowlist excludes it.
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
		// back creates a fresh manager instead of reusing this closed one —
		// and take its loaded mcp_* tools with it.
		unregisterLoadedMCPTools(agent, entry)
		if err := set.closeAndDrop(agentID); err != nil {
			logger.WarnCF("agent", "Failed to close MCP manager after paths changed",
				map[string]interface{}{"agent_id": agentID, "error": err.Error()})
		}
		agent.Tools.Unregister(tools.LoadMCPToolsName)
		entry, hasMgr = mcpManagerEntry{}, false
	}

	var servers []tools.MCPServerInfo
	if hasMgr {
		servers = entry.mgr.Servers()
		if len(servers) == 0 {
			// Reload removed every server: close the children, drop the
			// manager and take the loader AND its already-loaded mcp_* tools
			// away (otherwise stale remote tools would linger in the
			// registry with no manager to call them through).
			unregisterLoadedMCPTools(agent, entry)
			if err := set.closeAndDrop(agentID); err != nil {
				logger.WarnCF("agent", "Failed to close MCP manager with no servers left",
					map[string]interface{}{"agent_id": agentID, "error": err.Error()})
			}
			agent.Tools.Unregister(tools.LoadMCPToolsName)
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
			agent.Tools.Unregister(tools.LoadMCPToolsName)
			tc.setMCPServers(agent, nil)
			return
		}
		entry = mcpManagerEntry{mgr: mgr, paths: paths, registered: newNameSet()}
		set.put(agentID, entry)
	}

	// Servers are configuration, not tools: the prompt section is fed
	// regardless of the allowlist below, so the user still sees what is
	// configured even when load_mcp_tools itself was filtered out.
	tc.setMCPServers(agent, servers)

	if !allowlistKeeps(agentConfigForID(cfg, agentID), tools.LoadMCPToolsName) {
		// The allowlist tightened: the loader goes, and so do the mcp_*
		// tools it already registered — the loader is the only sanctioned
		// way to (re)create them, so keeping them would outlive the policy.
		unregisterLoadedMCPTools(agent, entry)
		agent.Tools.Unregister(tools.LoadMCPToolsName)
		return
	}
	if _, exists := agent.Tools.Get(tools.LoadMCPToolsName); exists {
		return // already registered against the same manager
	}
	// Wrap the manager's factory so every tool that lands in the agent's
	// registry through this loader is recorded in entry.registered.
	// LoadMCPToolsTool invokes the factory ONLY for names absent from the
	// registry (skip-if-present), so the recorded set is exactly the set this
	// agent registered through this entry — the source of truth used by
	// unregisterLoadedMCPTools at retirement.
	factory := entry.mgr.ToolFactory()
	agent.Tools.Register(tools.NewLoadMCPToolsTool(entry.mgr, agent.Tools,
		func(spec tools.LoadedMCPTool) tools.Tool {
			entry.registered.add(spec.Name)
			return factory(spec)
		}))
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
