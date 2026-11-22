package agent

import (
	"sort"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

type AgentRegistry struct {
	agents                 map[string]*AgentInstance
	resolver               *routing.RouteResolver
	mu                     sync.RWMutex
	sharedSessionManager   *session.SessionManager
	toolsAllowlists        map[string][]string
	folderResolver         func(sessionKey string) string
	subagentLedgerResolver func(agentID, sessionKey string) string
}

func NewAgentRegistry(cfg *config.Config) *AgentRegistry {
	registry := &AgentRegistry{
		agents:          make(map[string]*AgentInstance),
		resolver:        routing.NewRouteResolver(cfg),
		toolsAllowlists: make(map[string][]string),
	}

	agentConfigs := cfg.Agents.List
	if len(agentConfigs) == 0 {
		implicitAgent := &config.AgentConfig{
			ID:      "main",
			Default: true,
		}
		instance := NewAgentInstance(implicitAgent, &cfg.Agents.Defaults, cfg)
		registry.agents["main"] = instance
		registry.rememberToolsAllowlistLocked("main", implicitAgent.Tools)
	} else {
		for i := range agentConfigs {
			ac := &agentConfigs[i]
			id := routing.NormalizeAgentID(ac.ID)
			instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg)
			registry.agents[id] = instance
			registry.rememberToolsAllowlistLocked(id, ac.Tools)
		}
	}

	return registry
}

func (r *AgentRegistry) rememberToolsAllowlistLocked(id string, allowed []string) {
	if len(allowed) == 0 {
		delete(r.toolsAllowlists, id)
		return
	}
	if r.toolsAllowlists == nil {
		r.toolsAllowlists = make(map[string][]string)
	}
	r.toolsAllowlists[id] = cloneStrings(allowed)
}

func (r *AgentRegistry) toolsAllowlistLocked(id string) []string {
	return r.toolsAllowlists[id]
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

func (r *AgentRegistry) GetAgent(agentID string) (*AgentInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id := routing.NormalizeAgentID(agentID)
	agent, ok := r.agents[id]
	return agent, ok
}

func (r *AgentRegistry) ResolveRoute(input routing.RouteInput) routing.ResolvedRoute {
	return r.resolver.ResolveRoute(input)
}

func (r *AgentRegistry) ListAgentIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	return ids
}

func (r *AgentRegistry) SetSharedSessionManager(sm *session.SessionManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sharedSessionManager = sm
	if sm != nil {
		for _, agent := range r.agents {
			agent.Sessions = sm
		}
	}
}

func (r *AgentRegistry) SetFolderResolver(fn func(sessionKey string) string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.folderResolver = fn
	for _, agent := range r.agents {
		r.applyFolderResolverLocked(agent)
	}
}

func (r *AgentRegistry) applyFolderResolverLocked(instance *AgentInstance) {
	if instance == nil || instance.ContextBuilder == nil {
		return
	}
	instance.ContextBuilder.SetFolderResolver(r.folderResolver)
}

func (r *AgentRegistry) SetSubagentLedgerResolver(fn func(agentID, sessionKey string) string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subagentLedgerResolver = fn
	for _, agent := range r.agents {
		r.applySubagentLedgerResolverLocked(agent)
	}
}

func (r *AgentRegistry) applySubagentLedgerResolverLocked(instance *AgentInstance) {
	if instance == nil || instance.ContextBuilder == nil {
		return
	}
	fn := r.subagentLedgerResolver
	if fn == nil {
		instance.ContextBuilder.SetSubagentLedgerResolver(nil)
		return
	}
	id := instance.ID
	instance.ContextBuilder.SetSubagentLedgerResolver(func(sessionKey string) string {
		return fn(id, sessionKey)
	})
}

func (r *AgentRegistry) SetExecWhitelist(commands []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, agent := range r.agents {
		if agent.Tools == nil {
			continue
		}
		if tool, ok := agent.Tools.Get("exec"); ok {
			if et, ok := tool.(*tools.ExecTool); ok {
				et.SetWhitelist(commands)
			}
		}
	}
}

func (r *AgentRegistry) CanSpawnSubagent(parentAgentID, targetAgentID string) bool {
	parent, ok := r.GetAgent(parentAgentID)
	if !ok {
		return false
	}

	if parent.Subagents != nil && parent.Subagents.AllowAgents != nil {
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

	if parent.ID == "main" || parent.ID == routing.DefaultAgentID {
		_, exists := r.GetAgent(targetAgentID)
		return exists
	}

	return false
}

func (r *AgentRegistry) GetDefaultAgent() *AgentInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if agent, ok := r.agents["main"]; ok {
		return agent
	}

	for _, agent := range r.agents {
		if agent.IsDefault {
			return agent
		}
	}

	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 0 {
		return r.agents[ids[0]]
	}
	return nil
}

func (r *AgentRegistry) ReloadAgents(cfg *config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.resolver = routing.NewRouteResolver(cfg)

	agentConfigs := cfg.Agents.List
	if len(agentConfigs) == 0 {
		for id, instance := range r.agents {
			if id != "main" {
				logActiveSessions(id, instance)
				logger.InfoCF("agent", "Removing agent from registry (empty agents.list)",
					map[string]interface{}{
						"agent_id": id,
					})
				delete(r.agents, id)
				delete(r.toolsAllowlists, id)
			}
		}
		if _, ok := r.agents["main"]; !ok {
			implicitAgent := &config.AgentConfig{
				ID:      "main",
				Default: true,
			}
			instance := NewAgentInstance(implicitAgent, &cfg.Agents.Defaults, cfg)
			r.applyFolderResolverLocked(instance)
			r.applySubagentLedgerResolverLocked(instance)
			r.agents["main"] = instance
			r.rememberToolsAllowlistLocked("main", implicitAgent.Tools)
		}
		return
	}

	newIDs := make(map[string]bool)
	for i := range agentConfigs {
		id := routing.NormalizeAgentID(agentConfigs[i].ID)
		newIDs[id] = true
	}

	for id, instance := range r.agents {
		if !newIDs[id] {
			logActiveSessions(id, instance)
			delete(r.agents, id)
			delete(r.toolsAllowlists, id)
		}
	}

	for i := range agentConfigs {
		ac := &agentConfigs[i]
		id := routing.NormalizeAgentID(ac.ID)

		if existing, ok := r.agents[id]; ok && !agentConfigChanged(existing, ac, &cfg.Agents.Defaults, cfg, r.toolsAllowlistLocked(id)) {
			if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
				available := resolveAvailableSubagents(ac, cfg)
				existing.ContextBuilder.SetAvailableSubagents(available)
			}
			r.applyFolderResolverLocked(existing)
			r.applySubagentLedgerResolverLocked(existing)
			continue
		}

		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg)

		if old, ok := r.agents[id]; ok {
			instance.Sessions = old.Sessions
			instance.ContextBuilder = old.ContextBuilder
			old.ContextBuilder.SetSkillsFilter(ac.Skills)
			if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
				available := resolveAvailableSubagents(ac, cfg)
				old.ContextBuilder.SetAvailableSubagents(available)
			}
		} else if r.sharedSessionManager != nil {
			instance.Sessions = r.sharedSessionManager
		}

		r.applyFolderResolverLocked(instance)
		r.applySubagentLedgerResolverLocked(instance)

		r.agents[id] = instance
		r.rememberToolsAllowlistLocked(id, ac.Tools)
	}
}

func agentConfigChanged(existing *AgentInstance, ac *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config, existingTools []string) bool {
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
	if existing.Name != ac.Name {
		return true
	}
	if existing.Description != ac.Description {
		return true
	}
	if existing.IsDefault != ac.Default {
		return true
	}
	newMaxIter := defaults.MaxToolIterations
	if newMaxIter == 0 {
		newMaxIter = 20
	}
	if existing.MaxIterations != newMaxIter {
		return true
	}
	newMaxTokens := defaults.MaxTokens
	if newMaxTokens == 0 {
		newMaxTokens = 8192
	}
	if existing.MaxTokens != newMaxTokens {
		return true
	}
	if (existing.Subagents == nil) != (ac.Subagents == nil) {
		return true
	}
	if existing.Subagents != nil && ac.Subagents != nil {
		existingModel := ""
		newModel := ""
		if existing.Subagents.Model != nil {
			existingModel = existing.Subagents.Model.Primary
		}
		if ac.Subagents.Model != nil {
			newModel = ac.Subagents.Model.Primary
		}
		if existingModel != newModel {
			return true
		}
	}
	if !sameStringSet(existing.SkillsFilter, ac.Skills) {
		return true
	}
	if !sameStringSet(existingTools, ac.Tools) {
		return true
	}
	newTemperature := 0.7
	if defaults.Temperature != nil {
		newTemperature = *defaults.Temperature
	}
	if ac != nil && ac.Temperature != nil {
		newTemperature = *ac.Temperature
	}
	if existing.Temperature != newTemperature {
		return true
	}
	newFallbacks := resolveAgentFallbacks(ac, defaults, cfg)
	if !stringSlicesEqual(existing.Fallbacks, newFallbacks) {
		return true
	}
	newCtxWindow := getContextWindow(cfg, newModel, newProvider)
	if existing.ContextWindow != newCtxWindow {
		return true
	}
	newSupportsImages := getSupportsImages(cfg, newModel, newProvider)
	newSupportsVideo := getSupportsVideo(cfg, newModel, newProvider)
	if existing.SupportsImages != newSupportsImages || existing.SupportsVideo != newSupportsVideo {
		return true
	}
	newReasoning := getReasoningConfig(cfg, newModel, newProvider)
	if !reasoningConfigsEqual(existing.Reasoning, newReasoning) {
		return true
	}
	if existing.ThinkingLevel != resolveAgentThinkingLevel(ac, defaults) {
		return true
	}
	return false
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringSet(a, b []string) bool {
	setA, setB := toNameSet(a), toNameSet(b)
	if len(setA) != len(setB) {
		return false
	}
	for name := range setA {
		if _, ok := setB[name]; !ok {
			return false
		}
	}
	return true
}

func toNameSet(s []string) map[string]struct{} {
	set := make(map[string]struct{}, len(s))
	for _, v := range s {
		set[v] = struct{}{}
	}
	return set
}

func reasoningConfigsEqual(a, b *config.ReasoningConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Enable != b.Enable {
		return false
	}
	if !ptrStringsEqual(a.Effort, b.Effort) {
		return false
	}
	if !ptrStringsEqual(a.Summary, b.Summary) {
		return false
	}
	return true
}

func ptrStringsEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func resolveAgentModelForReload(ac *config.AgentConfig, defaults *config.AgentDefaults, cfg *config.Config) string {
	if ac != nil && ac.Model != nil && strings.TrimSpace(ac.Model.Primary) != "" {
		return cfg.Providers.ResolveModelAlias(strings.TrimSpace(ac.Model.Primary), defaults.Provider)
	}
	return cfg.Providers.ResolveModelAlias(defaults.Model, defaults.Provider)
}

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
