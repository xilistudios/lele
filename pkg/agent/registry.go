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

// AgentRegistry manages multiple agent instances and routes messages to them.
type AgentRegistry struct {
	agents               map[string]*AgentInstance
	resolver             *routing.RouteResolver
	mu                   sync.RWMutex
	sharedSessionManager *session.SessionManager // optionally set by AgentLoop
	// toolsAllowlists records the raw `tools:` allowlist (agents.list[].tools)
	// that produced each instance, keyed by normalized agent ID. The reload diff
	// needs the *config* content, not the resulting tool set: an allowlist is
	// applied by NewAgentInstance (nil/empty = all tools, unknown names are
	// ignored, a non-matching allowlist keeps everything) and ReloadRegistry
	// re-registers shared tools (spawn, web, background exec) on top of the
	// fresh instance, so comparing the live ToolRegistry against the new config
	// would report a change on every reload and never converge. AgentInstance
	// has no field for the allowlist and pkg/agent/instance.go is owned by
	// another task, so the baseline lives here. Slices are cloned because the
	// caller may keep mutating the config it passed.
	toolsAllowlists map[string][]string
	// folderResolver is propagated to every agent's ContextBuilder so each
	// session's system prompt can inject the folder the user selected for it
	// (WebUI "per-session folder context"). Set by AgentLoop after the
	// registry is created; applied to instances as they are created.
	folderResolver func(sessionKey string) string
}

// NewAgentRegistry creates a registry from config, instantiating all agents.
// Each agent creates its own provider based on its model configuration.
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

// rememberToolsAllowlistLocked records the `tools:` allowlist that produced an
// instance, keyed by normalized agent ID. nil/empty clears the entry because
// both mean "all tools" and must never be told apart by the reload diff.
// Caller must hold r.mu.
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

// toolsAllowlistLocked returns the recorded allowlist baseline for id. A nil
// map (registry built as a bare literal, e.g. in tests) reads as "no allowlist".
// Caller must hold r.mu.
func (r *AgentRegistry) toolsAllowlistLocked(id string) []string {
	return r.toolsAllowlists[id]
}

// cloneStrings copies a string slice so the registry never aliases the caller's
// config (which may keep being mutated after a reload). nil in, nil out.
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
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

// SetSharedSessionManager replaces every agent's individual session manager
// with the given shared instance, ensuring all agents operate on the same
// session storage. New agents created during ReloadAgents also receive it.
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

// SetFolderResolver stores fn and wires it into every current agent's
// ContextBuilder so per-session folder context reaches the system prompt.
// Agents created later (ReloadAgents) inherit the same resolver, which is how
// the loop's attachFolderResolver reaches builders created inside
// NewAgentInstance without instance.go seeing the loop.
func (r *AgentRegistry) SetFolderResolver(fn func(sessionKey string) string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.folderResolver = fn
	for _, agent := range r.agents {
		r.applyFolderResolverLocked(agent)
	}
}

// applyFolderResolverLocked wires the stored resolver into one instance.
// A nil resolver is propagated as well: it means "folder injection disabled",
// and clearing must reach builders that were already wired.
// Caller must hold r.mu.
func (r *AgentRegistry) applyFolderResolverLocked(instance *AgentInstance) {
	if instance == nil || instance.ContextBuilder == nil {
		return
	}
	instance.ContextBuilder.SetFolderResolver(r.folderResolver)
}

// SetExecWhitelist applies (or re-applies) the exec command whitelist to every
// agent's exec tool without rebuilding the registry. Used when the user adds a
// command to the whitelist from the TUI approval prompt, so the decision takes
// effect on the very next tool call.
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

// CanSpawnSubagent checks if parentAgentID is allowed to spawn targetAgentID.
func (r *AgentRegistry) CanSpawnSubagent(parentAgentID, targetAgentID string) bool {
	parent, ok := r.GetAgent(parentAgentID)
	if !ok {
		return false
	}

	// If parent has explicit allowlist, use it
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

	// No explicit allowlist - check if parent is default agent
	// Default agent can spawn any existing agent
	if parent.ID == "main" || parent.ID == routing.DefaultAgentID {
		// Check if target agent exists in registry
		_, exists := r.GetAgent(targetAgentID)
		return exists
	}

	// Non-default agents without allowlist cannot spawn
	return false
}

// GetDefaultAgent returns the default agent instance.
func (r *AgentRegistry) GetDefaultAgent() *AgentInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// First check for agent "main" (backward compatibility)
	if agent, ok := r.agents["main"]; ok {
		return agent
	}

	// Then iterate through all agents and return the one with IsDefault=true
	for _, agent := range r.agents {
		if agent.IsDefault {
			return agent
		}
	}

	// If no default found, fall back to the first alphabetically sorted agent
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
			r.agents["main"] = instance
			r.rememberToolsAllowlistLocked("main", implicitAgent.Tools)
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
			delete(r.agents, id)
			delete(r.toolsAllowlists, id)
		}
	}

	// Create or update agent instances from current config.
	// Only recreate if the effective config (model, workspace, provider) changed.
	for i := range agentConfigs {
		ac := &agentConfigs[i]
		id := routing.NormalizeAgentID(ac.ID)

		if existing, ok := r.agents[id]; ok && !agentConfigChanged(existing, ac, &cfg.Agents.Defaults, cfg, r.toolsAllowlistLocked(id)) {
			// Agent config unchanged — but subagent list may have changed if
			// another agent was added/removed/updated, so refresh it.
			if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
				available := resolveAvailableSubagents(ac, cfg)
				existing.ContextBuilder.SetAvailableSubagents(available)
			}
			// Preserved builder already carries the resolver; re-attach so a
			// builder created before SetFolderResolver also gets it (no-op
			// otherwise).
			r.applyFolderResolverLocked(existing)
			continue
		}

		instance := NewAgentInstance(ac, &cfg.Agents.Defaults, cfg)

		// If replacing an existing agent, migrate its session manager so active
		// conversations are not lost. The session manager persists to disk, but
		// in-flight sessions hold a reference to the old manager instance.
		if old, ok := r.agents[id]; ok {
			instance.Sessions = old.Sessions
			instance.ContextBuilder = old.ContextBuilder
			// The preserved builder still carries the skills allowlist the OLD
			// instance was built with (SetSkillsFilter is the only writer, and
			// NewAgentInstance configures the builder it discards here). Without
			// this refresh a skills change would be detected, the instance
			// recreated, and the agent would STILL render the old <skills> block
			// — the exact symptom the content diff exists to fix. Same semantics
			// as the fresh path in NewAgentInstance: nil/empty = all skills.
			old.ContextBuilder.SetSkillsFilter(ac.Skills)
			// Refresh subagents on the preserved ContextBuilder since the
			// agent list may have changed.
			if ac.Subagents != nil && len(ac.Subagents.AllowAgents) > 0 {
				available := resolveAvailableSubagents(ac, cfg)
				old.ContextBuilder.SetAvailableSubagents(available)
			}
		} else if r.sharedSessionManager != nil {
			// New agent: use the shared session manager if one is configured.
			instance.Sessions = r.sharedSessionManager
		}

		// Newly created builders must see the loop's folder resolver too.
		r.applyFolderResolverLocked(instance)

		r.agents[id] = instance
		r.rememberToolsAllowlistLocked(id, ac.Tools)
	}
}

// agentConfigChanged returns true if the effective configuration of an agent
// has changed and the instance needs to be recreated.
//
// existingTools is the raw `tools:` allowlist recorded for the instance when it
// was built (see AgentRegistry.toolsAllowlists). It cannot be derived from
// existing.Tools: the live registry is the allowlist applied to the base tools
// and then re-filtered over the shared tools registered by the tool
// coordinator, so its name set is larger than the allowlist and comparing it
// against ac.Tools would flag a change on every reload.
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
	// Identity fields are editable from the WebUI agent pages and surface
	// through GetAgentInfo (runtime), so a change here must recreate the
	// instance; otherwise the UI keeps showing a stale name/description and
	// the wrong agent is reported as default until restart.
	if existing.Name != ac.Name {
		return true
	}
	if existing.Description != ac.Description {
		return true
	}
	if existing.IsDefault != ac.Default {
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
	// Check subagents model override
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
	// Check skills filter — by CONTENT, order-insensitive. A length-only
	// comparison let ["a","b"] -> ["c","d"] pass as "unchanged", so the agent
	// kept serving the old skills until restart. Same set in different order is
	// NOT a change (the skills block is rendered from the loader's own order).
	if !sameStringSet(existing.SkillsFilter, ac.Skills) {
		return true
	}
	// Check tools allowlist — by CONTENT, order-insensitive, against the
	// allowlist recorded at instance-build time (nil and [] are both "all
	// tools" and must not look like a change).
	if !sameStringSet(existingTools, ac.Tools) {
		return true
	}
	// Check temperature — same resolution logic as NewAgentInstance:
	// agent config overrides defaults, default is 0.7
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
	// Check fallbacks — compare resolved fallback lists
	newFallbacks := resolveAgentFallbacks(ac, defaults, cfg)
	if !stringSlicesEqual(existing.Fallbacks, newFallbacks) {
		return true
	}
	// Check context window — computed from provider model config
	newCtxWindow := getContextWindow(cfg, newModel, newProvider)
	if existing.ContextWindow != newCtxWindow {
		return true
	}
	// Check supports images — computed from provider model config
	newSupportsImages := getSupportsImages(cfg, newModel, newProvider)
	if existing.SupportsImages != newSupportsImages {
		return true
	}
	// Check reasoning config — computed from provider model config
	newReasoning := getReasoningConfig(cfg, newModel, newProvider)
	if !reasoningConfigsEqual(existing.Reasoning, newReasoning) {
		return true
	}
	// Check thinking level — same resolution logic as NewAgentInstance:
	// agent config overrides defaults, invalid/unset resolves to "". Without
	// this, saving a new thinking_level in config.json + ReloadRegistry would
	// keep the old instance and the stale level.
	if existing.ThinkingLevel != resolveAgentThinkingLevel(ac, defaults) {
		return true
	}
	return false
}

// stringSlicesEqual returns true if both slices have the same length and elements.
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

// sameStringSet reports whether two string slices hold the same set of values,
// ignoring order and duplicates. nil and empty are equal (both mean "no
// allowlist"), which is what the per-agent `tools:` field requires: nil and []
// both resolve to "all tools". Used for membership-style allowlists (skills,
// tools), whose consumers test containment and never index or multiplicity.
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

// toNameSet folds a string slice into a set, dropping order and duplicates.
func toNameSet(s []string) map[string]struct{} {
	set := make(map[string]struct{}, len(s))
	for _, v := range s {
		set[v] = struct{}{}
	}
	return set
}

// reasoningConfigsEqual compares two ReasoningConfig values for equality.
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

// ptrStringsEqual compares two *string values for equality.
func ptrStringsEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
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
