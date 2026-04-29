package agent

import (
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/routing"
)

// AgentRegistry manages multiple agent instances and routes messages to them.
type AgentRegistry struct {
	agents   map[string]*AgentInstance
	resolver *routing.RouteResolver
	mu       sync.RWMutex
}

// NewAgentRegistry creates a registry from config, instantiating all agents.
// Each agent creates its own provider based on its model configuration.
func NewAgentRegistry(cfg *config.Config) *AgentRegistry {
	registry := &AgentRegistry{
		agents:   make(map[string]*AgentInstance),
		resolver: routing.NewRouteResolver(cfg),
	}

	agentConfigs := cfg.Agents.List
	if len(agentConfigs) == 0 {
		implicitAgent := &config.AgentConfig{
			ID:      "main",
			Default: true,
		}
		instance := NewAgentInstance(implicitAgent, &cfg.Agents.Defaults, cfg)
		registry.agents["main"] = instance
		logger.InfoCF("agent", "Created implicit main agent (no agents.list configured)", nil)
	} else {
		for i := range agentConfigs {
			ac := &agentConfigs[i]
			id := routing.NormalizeAgentID(ac.ID)
			instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg)
			registry.agents[id] = instance
			logger.InfoCF("agent", "Registered agent",
				map[string]interface{}{
					"agent_id":  id,
					"name":      ac.Name,
					"workspace": instance.Workspace,
					"model":     instance.Model,
					"provider":  extractProviderFromModel(instance.Model, cfg.Agents.Defaults.Provider),
				})
		}
	}

	return registry
}

// GetAgent returns the agent instance for a given ID.
func (r *AgentRegistry) GetAgent(agentID string) (*AgentInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := routing.NormalizeAgentID(agentID)
	agent, ok := r.agents[id]
	return agent, ok
}

// ResolveRoute determines which agent handles the message.
func (r *AgentRegistry) ResolveRoute(input routing.RouteInput) routing.ResolvedRoute {
	return r.resolver.ResolveRoute(input)
}

// ListAgentIDs returns all registered agent IDs.
func (r *AgentRegistry) ListAgentIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	return ids
}

// CanSpawnSubagent checks if parentAgentID is allowed to spawn targetAgentID.
func (r *AgentRegistry) CanSpawnSubagent(parentAgentID, targetAgentID string) bool {
	parent, ok := r.GetAgent(parentAgentID)
	if !ok {
		return false
	}
	if parent.Subagents == nil || parent.Subagents.AllowAgents == nil {
		return false
	}
	targetNorm := routing.NormalizeAgentID(targetAgentID)
	for _, allowed := range parent.Subagents.AllowAgents {
		if allowed == "*" {
			return true
		}
		if routing.NormalizeAgentID(allowed) == targetNorm {
			return true
		}
	}
	return false
}

// GetDefaultAgent returns the default agent instance.
func (r *AgentRegistry) GetDefaultAgent() *AgentInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if agent, ok := r.agents["main"]; ok {
		return agent
	}
	for _, agent := range r.agents {
		return agent
	}
	return nil
}

// ReloadAgents updates the registry with new agent configurations.
// It only recreates agent instances whose effective configuration has changed.
// Agents that no longer exist in the config are removed (with a warning if they
// have active sessions). New agents are created and existing unchanged agents
// are preserved along with their in-memory sessions.
func (r *AgentRegistry) ReloadAgents(cfg *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update the route resolver
	r.resolver = routing.NewRouteResolver(cfg)

	agentConfigs := cfg.Agents.List
	if len(agentConfigs) == 0 {
		// No agents configured — remove existing agents and ensure main agent exists.
		for id, instance := range r.agents {
			if id != "main" {
				logActiveSessions(id, instance)
				logger.InfoCF("agent", "Removing agent from registry (empty agents.list)",
					map[string]interface{}{
						"agent_id": id,
					})
				delete(r.agents, id)
			}
		}
		if _, ok := r.agents["main"]; !ok {
			implicitAgent := &config.AgentConfig{
				ID:      "main",
				Default: true,
			}
			instance := NewAgentInstance(implicitAgent, &cfg.Agents.Defaults, cfg)
			r.agents["main"] = instance
			logger.InfoCF("agent", "Created implicit main agent (no agents.list configured)", nil)
		}
		return
	}

	// Build set of new agent IDs
	newIDs := make(map[string]bool)
	for i := range agentConfigs {
		id := routing.NormalizeAgentID(agentConfigs[i].ID)
		newIDs[id] = true
	}

	// Remove agents that no longer exist in config
	for id, instance := range r.agents {
		if !newIDs[id] {
			logActiveSessions(id, instance)
			logger.InfoCF("agent", "Removing agent from registry", map[string]interface{}{
				"agent_id": id,
			})
			delete(r.agents, id)
		}
	}

	// Create or update agent instances from current config.
	// Only recreate if the effective config (model, workspace, provider) changed.
	for i := range agentConfigs {
		ac := &agentConfigs[i]
		id := routing.NormalizeAgentID(ac.ID)

		if existing, ok := r.agents[id]; ok && !agentConfigChanged(existing, ac, &cfg.Agents.Defaults, cfg) {
			// Agent config unchanged — preserve existing instance (keeps sessions alive)
			continue
		}

		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg)

		// If replacing an existing agent, migrate its session manager so active
		// conversations are not lost. The session manager persists to disk, but
		// in-flight sessions hold a reference to the old manager instance.
		if old, ok := r.agents[id]; ok {
			instance.Sessions = old.Sessions
			instance.ContextBuilder = old.ContextBuilder
			logger.InfoCF("agent", "Updated agent (config changed), migrated sessions",
				map[string]interface{}{
					"agent_id": id,
				})
		}

		r.agents[id] = instance
		logger.InfoCF("agent", "Registered agent",
			map[string]interface{}{
				"agent_id":  id,
				"name":      ac.Name,
				"workspace": instance.Workspace,
				"model":     instance.Model,
				"provider":  extractProviderFromModel(instance.Model, cfg.Agents.Defaults.Provider),
			})
	}
}

// agentConfigChanged returns true if the effective configuration of an agent
// has changed and the instance needs to be recreated.
func agentConfigChanged(existing *AgentInstance, ac *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) bool {
	newModel := resolveAgentModelForReload(ac, defaults, cfg)
	newWorkspace := resolveAgentWorkspace(ac, defaults)
	newProvider := extractProviderFromModel(newModel, defaults.Provider)

	if existing.Model != newModel {
		return true
	}
	if existing.Workspace != newWorkspace {
		return true
	}
	if extractProviderFromModel(existing.Model, defaults.Provider) != newProvider {
		return true
	}
	// Check max iterations
	newMaxIter := defaults.MaxToolIterations
	if newMaxIter == 0 {
		newMaxIter = 20
	}
	if existing.MaxIterations != newMaxIter {
		return true
	}
	// Check max tokens
	newMaxTokens := defaults.MaxTokens
	if newMaxTokens == 0 {
		newMaxTokens = 8192
	}
	if existing.MaxTokens != newMaxTokens {
		return true
	}
	// Check subagents config
	if (existing.Subagents == nil) != (ac.Subagents == nil) {
		return true
	}
	// Check skills filter
	if len(existing.SkillsFilter) != len(ac.Skills) {
		return true
	}

	return false
}

// resolveAgentModelForReload resolves the model for an agent during registry reload.
// This is a simplified version that avoids the debug logging in resolveAgentModel.
func resolveAgentModelForReload(ac *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) string {
	if ac != nil && ac.Model != nil && strings.TrimSpace(ac.Model.Primary) != "" {
		return cfg.Providers.ResolveModelAlias(strings.TrimSpace(ac.Model.Primary), defaults.Provider)
	}
	return cfg.Providers.ResolveModelAlias(defaults.Model, defaults.Provider)
}

// logActiveSessions warns if an agent being removed has active sessions.
func logActiveSessions(agentID string, instance *AgentInstance) {
	if instance == nil || instance.Sessions == nil {
		return
	}
	count := instance.Sessions.ActiveCount()
	if count > 0 {
		logger.WarnCF("agent", "Removing agent with active sessions — existing conversations may be disrupted",
			map[string]interface{}{
				"agent_id":        agentID,
				"active_sessions": count,
			})
	}
}
