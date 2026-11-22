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

type mcpManager interface {
	Servers() []tools.MCPServerInfo
	LoadServer(ctx context.Context, server string) ([]tools.LoadedMCPTool, error)
	ToolUsage(server string) map[string]uint64
	ToolFactory() func(tools.LoadedMCPTool) tools.Tool
	Close() error
}

type mcpManagerEntry struct {
	mgr   mcpManager
	paths mcp.Paths
}

type mcpLayerFlags struct {
	list bool
	get  bool
}

type mcpLayerPerms struct {
	flags mcpLayerFlags
	gen   uint64
}

type mcpManagerSet struct {
	syncMu     sync.Mutex
	mu         sync.Mutex
	entries    map[string]mcpManagerEntry
	stopped    bool
	cwd        string
	newManager func(mcp.Paths) mcpManager
	layers     *sessionToolSet
	layerFlags map[string]mcpLayerPerms
}

func newMCPManagerSet() *mcpManagerSet {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return &mcpManagerSet{
		entries:    make(map[string]mcpManagerEntry),
		layerFlags: make(map[string]mcpLayerPerms),
		layers:     newSessionToolSet(),
		cwd:        cwd,
		newManager: func(paths mcp.Paths) mcpManager {
			return mcp.NewManager(paths, nil)
		},
	}
}

func (s *mcpManagerSet) pathsFor(agent *AgentInstance) mcp.Paths {
	return mcp.Paths{
		LeleDir:        config.GetLeleDir(),
		AgentWorkspace: agent.Workspace,
		Cwd:            s.cwd,
	}
}

func (s *mcpManagerSet) get(agentID string) (mcpManagerEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[agentID]
	return entry, ok
}

func (s *mcpManagerSet) put(agentID string, entry mcpManagerEntry) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		closeManagerQuietly(agentID, entry.mgr)
		return
	}
	previous, had := s.entries[agentID]
	s.entries[agentID] = entry
	replaced := had && previous.mgr != entry.mgr
	if replaced {
		delete(s.layerFlags, agentID)
	}
	s.mu.Unlock()
	if replaced {
		s.layers.dropAgent(agentID)
		closeManagerQuietly(agentID, previous.mgr)
	}
}

func (s *mcpManagerSet) closeAndDrop(agentID string) error {
	s.mu.Lock()
	entry, ok := s.entries[agentID]
	delete(s.entries, agentID)
	delete(s.layerFlags, agentID)
	s.mu.Unlock()
	s.layers.dropAgent(agentID)
	if !ok {
		return nil
	}
	return entry.mgr.Close()
}

func (s *mcpManagerSet) dropSessionLayers(agentID string) {
	s.mu.Lock()
	delete(s.layerFlags, agentID)
	s.mu.Unlock()
	s.layers.dropAgent(agentID)
}

func (s *mcpManagerSet) sessionTools(agent *AgentInstance, sessionKey string, allowList, allowGet bool) *tools.ToolRegistry {
	if agent == nil || agent.Tools == nil {
		return nil
	}
	if sessionKey == "" {
		return agent.Tools
	}
	entry, ok := s.get(agent.ID)
	if !ok || entry.mgr == nil {
		return agent.Tools
	}

	flags := mcpLayerFlags{list: allowList, get: allowGet}
	gen := s.observePermissions(agent.ID, flags)

	registry := s.layers.getOrCreate(agent.ID, sessionKey, gen, func() *tools.ToolRegistry {
		layer := tools.NewScopedRegistry(agent.Tools)
		if allowList {
			layer.Register(tools.NewListMCPToolsTool(entry.mgr))
		}
		if allowGet {
			layer.Register(tools.NewGetMCPToolTool(entry.mgr, layer, entry.mgr.ToolFactory()))
		}
		return layer
	})

	if !s.permissionsCurrent(agent.ID, flags, gen) {
		return agent.Tools
	}
	return registry
}

func (s *mcpManagerSet) observePermissions(agentID string, flags mcpLayerFlags) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, known := s.layerFlags[agentID]
	if known && record.flags == flags {
		return record.gen
	}
	record = mcpLayerPerms{flags: flags, gen: record.gen + 1}
	s.layerFlags[agentID] = record
	return record.gen
}

func (s *mcpManagerSet) permissionsCurrent(agentID string, flags mcpLayerFlags, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, known := s.layerFlags[agentID]
	return known && current.flags == flags && current.gen == gen
}

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
			delete(s.layerFlags, id)
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

func (s *mcpManagerSet) closeAll() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	s.mu.Lock()
	s.stopped = true
	entries := s.entries
	s.entries = make(map[string]mcpManagerEntry)
	s.layerFlags = make(map[string]mcpLayerPerms)
	s.mu.Unlock()

	s.layers.dropAll()

	var errs []error
	for id, entry := range entries {
		if err := entry.mgr.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp manager %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

func (s *mcpManagerSet) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func closeManagerQuietly(agentID string, mgr mcpManager) {
	if mgr == nil {
		return
	}
	if err := mgr.Close(); err != nil {
		logger.WarnCF("agent", "Failed to close retired MCP manager",
			map[string]interface{}{"agent_id": agentID, "error": err.Error()})
	}
}

func (tc *toolCoordinatorImpl) syncMCPTools() {
	if tc.al == nil || tc.registry == nil || tc.mcpManagers == nil {
		return
	}
	tc.mcpManagers.syncMu.Lock()
	defer tc.mcpManagers.syncMu.Unlock()

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

func (tc *toolCoordinatorImpl) syncMCPServersForAgent(cfg *config.Config, agent *AgentInstance, agentID string) {
	set := tc.mcpManagers
	if agent == nil || agent.Tools == nil {
		return
	}
	paths := set.pathsFor(agent)

	allowList, allowGet := mcpDiscoveryAllowed(agentConfigForID(cfg, agentID))
	tc.setMCPDiscovery(agent, allowList, allowGet)

	entry, hasMgr := set.get(agentID)
	if hasMgr && entry.paths != paths {
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
			closeManagerQuietly(agentID, mgr)
			tc.setMCPServers(agent, nil)
			return
		}
		set.put(agentID, mcpManagerEntry{mgr: mgr, paths: paths})
	}

	tc.setMCPServers(agent, servers)

	if !allowList && !allowGet {
		set.dropSessionLayers(agentID)
	}
}

func mcpDiscoveryAllowed(cfg *config.AgentConfig) (list, get bool) {
	legacy := allowlistKeeps(cfg, tools.LoadMCPToolsName)
	list = allowlistKeeps(cfg, tools.ListMCPToolsName) || legacy
	get = allowlistKeeps(cfg, tools.GetMCPToolName) || legacy
	return list, get
}

func (tc *toolCoordinatorImpl) sessionTools(agent *AgentInstance, sessionKey string) *tools.ToolRegistry {
	if agent == nil || agent.Tools == nil {
		return nil
	}
	if tc.mcpManagers == nil {
		return agent.Tools
	}
	var cfg *config.Config
	if tc.al != nil {
		cfg = tc.al.cfg()
	}
	allowList, allowGet := mcpDiscoveryAllowed(agentConfigForID(cfg, agent.ID))
	if !allowList && !allowGet {
		return agent.Tools
	}
	registry := tc.mcpManagers.sessionTools(agent, sessionKey, allowList, allowGet)
	if tc.al != nil {
		if listNow, getNow := mcpDiscoveryAllowed(agentConfigForID(tc.al.cfg(), agent.ID)); listNow != allowList || getNow != allowGet {
			return agent.Tools
		}
	}
	return registry
}

func (tc *toolCoordinatorImpl) dropSessionTools(sessionKey string) {
	if tc.mcpManagers == nil || sessionKey == "" {
		return
	}
	tc.mcpManagers.layers.dropSession(sessionKey)
}

func (tc *toolCoordinatorImpl) setMCPServers(agent *AgentInstance, servers []tools.MCPServerInfo) {
	if agent.ContextBuilder == nil {
		return
	}
	agent.ContextBuilder.SetMCPServers(servers)
}

func (tc *toolCoordinatorImpl) setMCPDiscovery(agent *AgentInstance, list, get bool) {
	if agent.ContextBuilder == nil {
		return
	}
	agent.ContextBuilder.SetMCPDiscovery(list, get)
}

func (tc *toolCoordinatorImpl) closeMCPManagers() error {
	if tc.mcpManagers == nil {
		return nil
	}
	return tc.mcpManagers.closeAll()
}

func (al *AgentLoop) CloseMCPManagers() error {
	if al.toolCoordinator == nil {
		return nil
	}
	return al.toolCoordinator.closeMCPManagers()
}

func (al *AgentLoop) SyncMCPServers() {
	if al.toolCoordinator == nil {
		return
	}
	al.toolCoordinator.syncMCPTools()
}

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
