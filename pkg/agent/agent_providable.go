// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
)

// agentProvidableImpl implements the channels.AgentProvidable interface.
// It delegates operations to the appropriate internal components.
type agentProvidableImpl struct {
	al *AgentLoop
}

// newAgentProvidable creates a new agent providable instance.
func newAgentProvidable(al *AgentLoop) *agentProvidableImpl {
	return &agentProvidableImpl{al: al}
}

// ============================================================================
// AgentSessionManager Interface (embedded in AgentProvidable)
// ============================================================================

// GetSessionAgent gets the active agent for a session.
func (ap *agentProvidableImpl) GetSessionAgent(sessionKey string) string {
	return ap.al.getSessionAgent(sessionKey)
}

// SetSessionAgent sets the active agent for a specific session.
func (ap *agentProvidableImpl) SetSessionAgent(sessionKey, agentID string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	currentAgentID := ap.GetSessionAgent(sessionKey)
	if currentAgentID == agentID {
		return
	}

	// Migrate session history from old agent to new agent
	if currentAgentID != "" {
		oldAgent, oldOk := ap.al.registry.GetAgent(currentAgentID)
		newAgent, newOk := ap.al.registry.GetAgent(agentID)
		if oldOk && newOk && oldAgent != nil && newAgent != nil {
			history := oldAgent.Sessions.GetHistory(resolvedKey)
			summary := oldAgent.Sessions.GetSummary(resolvedKey)
			name := oldAgent.Sessions.GetName(resolvedKey)
			verboseLevel := oldAgent.Sessions.GetVerboseLevel(resolvedKey)
			thinkingLevel := oldAgent.Sessions.GetThinkingLevel(resolvedKey)

			// Copy history to new agent's session manager
			if len(history) > 0 || summary != "" || name != "" || thinkingLevel != "" {
				newAgent.Sessions.GetOrCreate(resolvedKey)
				if len(history) > 0 {
					newAgent.Sessions.SetHistory(resolvedKey, history)
				}
				if summary != "" {
					newAgent.Sessions.SetSummary(resolvedKey, summary)
				}
				if name != "" {
					newAgent.Sessions.SetName(resolvedKey, name)
				}
				if verboseLevel != "off" {
					newAgent.Sessions.SetVerboseLevel(resolvedKey, verboseLevel)
				}
				if thinkingLevel != "" {
					newAgent.Sessions.SetThinkingLevel(resolvedKey, thinkingLevel)
				}
				logger.InfoCF("agent", "Migrated session history to new agent", map[string]interface{}{
					"session_key":   resolvedKey,
					"old_agent_id":  currentAgentID,
					"new_agent_id":  agentID,
					"history_count": len(history),
					"has_summary":   summary != "",
				})
			}
		}
	}

	ap.al.sessionAgents.Store(resolvedKey, agentID)
	ap.al.sessionModels.Delete(resolvedKey)

	// Clear the persisted model override in the shared session store.
	// Since all agents share the same SessionManager now, the old model
	// would otherwise survive across agent switches.
	if agent, ok := ap.al.registry.GetAgent(agentID); ok && agent != nil && agent.Sessions != nil {
		agent.Sessions.SetModel(resolvedKey, "")
	}
}

// ListAvailableAgentIDs returns the list of available agent IDs.
func (ap *agentProvidableImpl) ListAvailableAgentIDs() []string {
	return ap.al.registry.ListAgentIDs()
}

// GetDefaultAgentID returns the default agent ID.
func (ap *agentProvidableImpl) GetDefaultAgentID() string {
	return ap.al.getDefaultAgentID()
}

// ============================================================================
// AgentProvidable Interface - Agent Info
// ============================================================================

// GetAgentInfo returns basic agent info for the UI.
func (ap *agentProvidableImpl) GetAgentInfo(agentID string) (channels.AgentBasicInfo, bool) {
	agent, ok := ap.al.registry.GetAgent(agentID)
	if !ok {
		return channels.AgentBasicInfo{}, false
	}
	return channels.AgentBasicInfo{
		ID:             agent.ID,
		Name:           agent.Name,
		Model:          agent.Model,
		Workspace:      agent.Workspace,
		MaxIterations:  agent.MaxIterations,
		MaxTokens:      agent.MaxTokens,
		Temperature:    agent.Temperature,
		Fallbacks:      agent.Fallbacks,
		SkillsFilter:   agent.SkillsFilter,
		Reasoning:      agent.Reasoning,
		SupportsImages: agent.SupportsImages,
	}, true
}

// ============================================================================
// AgentProvidable Interface - Session History
// ============================================================================

// AddSessionMessage adds a message to the persisted session history.
func (ap *agentProvidableImpl) AddSessionMessage(sessionKey string, msg providers.Message) error {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return fmt.Errorf("no agent found for session: %s", resolvedSessionKey)
	}

	agent.Sessions.AddFullMessage(resolvedSessionKey, msg)
	return nil
}

// GetSessionHistory returns the persisted history for a session.
func (ap *agentProvidableImpl) GetSessionHistory(sessionKey string) []providers.Message {
	return ap.GetHistoryView(sessionKey)
}

// GetHistoryView returns the history slice without copying. The caller MUST
// NOT modify the returned slice or any message in it. Used by hot read paths
// (TUI viewport rendering, message counting, token estimation) where a full
// copy per render would allocate tens of MB for long conversations.
func (ap *agentProvidableImpl) GetHistoryView(sessionKey string) []providers.Message {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		// Fast path: O(1) lookup in the subagent-to-agent mapping
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				history := agent.Sessions.GetHistoryView(resolvedSessionKey)
				if len(history) > 0 {
					return history
				}
			}
		}

		// Fallback: O(N) scan over all agents (self-healing — caches first hit)
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			history := agent.Sessions.GetHistoryView(resolvedSessionKey)
			if len(history) > 0 {
				// Cache this mapping for future O(1) lookups
				ap.al.subagentSessionAgent.Store(resolvedSessionKey, agent.ID)
				return history
			}
		}
		return []providers.Message{}
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return []providers.Message{}
	}
	return agent.Sessions.GetHistoryView(resolvedSessionKey)
}

// LoadEvictedMessages re-inserts evicted (excluded) messages from SQLite
// back into memory, restoring full display history. Idempotent; no-op when
// nothing was evicted. Returns the number of messages loaded.
func (ap *agentProvidableImpl) LoadEvictedMessages(sessionKey string) int {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				return agent.Sessions.LoadEvictedMessages(resolvedSessionKey)
			}
		}
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			if agent.Sessions.GetEvictedMessageCount(resolvedSessionKey) > 0 {
				ap.al.subagentSessionAgent.Store(resolvedSessionKey, agent.ID)
				return agent.Sessions.LoadEvictedMessages(resolvedSessionKey)
			}
		}
		return 0
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0
	}
	return agent.Sessions.LoadEvictedMessages(resolvedSessionKey)
}

// GetEvictedMessageCount returns the number of messages that were evicted
// from memory (excluded + persisted in SQLite but not in the in-memory slice).
func (ap *agentProvidableImpl) GetEvictedMessageCount(sessionKey string) int {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				return agent.Sessions.GetEvictedMessageCount(resolvedSessionKey)
			}
		}
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			if count := agent.Sessions.GetEvictedMessageCount(resolvedSessionKey); count > 0 {
				ap.al.subagentSessionAgent.Store(resolvedSessionKey, agent.ID)
				return count
			}
		}
		return 0
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0
	}
	return agent.Sessions.GetEvictedMessageCount(resolvedSessionKey)
}

// GetTotalMessageCount returns the total persisted message count for a
// session: in-memory slice length plus evicted messages.
func (ap *agentProvidableImpl) GetTotalMessageCount(sessionKey string) int {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				return agent.Sessions.GetTotalMessageCount(resolvedSessionKey)
			}
		}
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			if count := agent.Sessions.GetTotalMessageCount(resolvedSessionKey); count > 0 {
				ap.al.subagentSessionAgent.Store(resolvedSessionKey, agent.ID)
				return count
			}
		}
		return 0
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0
	}
	return agent.Sessions.GetTotalMessageCount(resolvedSessionKey)
}

// ============================================================================
// AgentProvidable Interface - Model Management
// ============================================================================

// GetSessionModel returns the effective model for a session.
// It checks in order: 1) in-memory sessionModels, 2) persisted session model, 3) agent default.
func (ap *agentProvidableImpl) GetSessionModel(sessionKey string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return ""
	}
	// First check in-memory override (fast path)
	if model, ok := ap.al.sessionModels.Load(resolvedSessionKey); ok {
		if selected, ok := model.(string); ok && selected != "" {
			return selected
		}
	}
	// Then check persisted session model (survives restarts)
	if agent.Sessions != nil {
		persistedModel := agent.Sessions.GetModel(resolvedSessionKey)
		if persistedModel != "" {
			return persistedModel
		}
	}
	return agent.Model
}

// GetSessionMode returns the mode for a session ("chat", "agent", "group").
// Returns "" if not set (callers should normalize "" to "agent").
func (ap *agentProvidableImpl) GetSessionMode(sessionKey string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil || agent.Sessions == nil {
		return ""
	}
	return agent.Sessions.GetMode(resolvedSessionKey)
}

// SetSessionMode sets the mode for a session and persists it.
func (ap *agentProvidableImpl) SetSessionMode(sessionKey, mode string) error {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("no agent available for session")
	}
	return agent.Sessions.SetMode(resolvedSessionKey, mode)
}

// GetSessionModelSupportsImages returns true if the session's current model supports vision.
func (ap *agentProvidableImpl) GetSessionModelSupportsImages(sessionKey string) bool {
	model := ap.GetSessionModel(sessionKey)
	if model == "" {
		return false
	}
	agent := ap.al.agentForSession(ap.al.ResolveSessionKey(sessionKey))
	if agent == nil {
		return false
	}
	cfg := ap.al.cfg()
	providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
	return getSupportsImages(cfg, model, providerName)
}

// SetSessionModel sets the model for a session and persists it.
func (ap *agentProvidableImpl) SetSessionModel(sessionKey, model string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	if resolvedSessionKey == "" {
		return ""
	}
	next := ap.al.cfg().Providers.ResolveModelAlias(model, ap.al.cfg().Agents.Defaults.Provider)
	// Store in memory for fast access
	ap.al.sessionModels.Store(resolvedSessionKey, next)
	// Persist to session storage (survives restarts)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent != nil && agent.Sessions != nil {
		agent.Sessions.SetModel(resolvedSessionKey, next)
	}
	return next
}

// ListAvailableModels returns configured model aliases for the provider backing an agent.
func (ap *agentProvidableImpl) ListAvailableModels(agentID string) []string {
	providerName := ap.al.cfg().Agents.Defaults.Provider
	if agentID != "" {
		if agent, ok := ap.al.registry.GetAgent(agentID); ok && agent != nil {
			if ref := providers.ParseModelRef(agent.Model, ap.al.cfg().Agents.Defaults.Provider); ref != nil {
				providerName = ref.Provider
			}
		}
	}

	provider, ok := ap.al.cfg().Providers.GetNamed(providerName)
	if !ok || len(provider.Models) == 0 {
		return nil
	}

	models := make([]string, 0, len(provider.Models))
	for alias := range provider.Models {
		models = append(models, alias)
	}
	sort.Strings(models)
	return models
}

// ============================================================================
// AgentProvidable Interface - Config
// ============================================================================

// GetConfigSnapshot returns the current configuration snapshot.
func (ap *agentProvidableImpl) GetConfigSnapshot() *config.Config {
	return ap.al.cfg()
}

// ============================================================================
// AgentProvidable Interface - Status & Control
// ============================================================================

// GetStatus returns the current status for a session.
func (ap *agentProvidableImpl) GetStatus(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}
	// Delegate to message processor for formatting
	return ap.al.messageProcessor.formatStatusResponse(agent, sessionKey, "telegram")
}

// StopAgent stops the agent processing for a session.
func (ap *agentProvidableImpl) StopAgent(sessionKey string) string {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	logger.InfoCF("agent", "StopAgent called", map[string]interface{}{
		"session_key":   sessionKey,
		"resolved_key":  resolvedKey,
		"is_processing": ap.al.sessionManager.IsSessionProcessing(resolvedKey),
	})

	subagentCount := 0
	if ap.al.toolCoordinator != nil {
		subagentCount = ap.al.toolCoordinator.stopSessionSubagents(resolvedKey)
	}
	cancelled := ap.al.cancelSession(resolvedKey)
	groupCount := 0
	if gm := ap.al.GroupManager(); gm != nil {
		groupCount = gm.StopByOrigin("", resolvedKey)
		if sessionKey != "" && sessionKey != resolvedKey {
			groupCount += gm.StopByOrigin("", sessionKey)
		}
	}
	logger.InfoCF("agent", "StopAgent completed", map[string]interface{}{
		"session_key":    resolvedKey,
		"cancelled":      cancelled,
		"subagent_count": subagentCount,
		"group_count":    groupCount,
	})

	if groupCount > 0 && subagentCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d subagente(s) y %d grupo(s)).", subagentCount, groupCount)
	}
	if groupCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d grupo(s)).", groupCount)
	}
	if subagentCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d subagente(s)).", subagentCount)
	}
	return "⏹️ Agente detenido."
}

// CompactSession compacts the session history.
func (ap *agentProvidableImpl) CompactSession(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}

	if agent.Sessions.GetTotalMessageCount(sessionKey) <= 4 {
		return "📭 Not enough messages to compact (need 5+)."
	}

	// Register a session cancel so IsSessionProcessing returns true during
	// compaction. This keeps the TUI loading animation active while the LLM
	// summarization call runs (can take several seconds).
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	unregister := ap.al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
	defer unregister()
	_ = ctx // context is used internally by summarizeSession

	stats := ap.al.sessionManager.summarizeSession(agent, sessionKey)
	if stats == nil {
		return "❌ Compaction failed or nothing to compact."
	}

	return fmt.Sprintf("✅ Compacted session: %d messages → %d messages (%d tokens saved)",
		stats.BeforeMessages, stats.AfterMessages, stats.SavedTokens)
}

// ============================================================================
// AgentProvidable Interface - Verbose Management
// ============================================================================

// ToggleVerbose toggles verbose mode for a session.
func (ap *agentProvidableImpl) ToggleVerbose(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "Verbose mode requires a session context. Please start a conversation first."
	}
	newLevel := ap.al.verboseManager.CycleLevel(sessionKey)
	switch newLevel {
	case session.VerboseOff:
		return "🔇 Verbose mode **OFF**\nTool execution notifications are hidden."
	case session.VerboseBasic:
		return "🛠️ Verbose mode **BASIC**\nYou will see simplified tool execution notifications."
	case session.VerboseFull:
		return "📋 Verbose mode **FULL**\nYou will see detailed tool execution and results."
	}
	return "Unknown verbose level"
}

// GetVerboseLevel returns the current verbose level for a session.
func (ap *agentProvidableImpl) GetVerboseLevel(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "off"
	}
	return string(ap.al.verboseManager.GetLevel(sessionKey))
}

// SetVerboseLevel sets the verbose level for a session.
func (ap *agentProvidableImpl) SetVerboseLevel(sessionKey string, level string) bool {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return false
	}
	if !session.IsValidVerboseLevel(level) {
		return false
	}
	ap.al.verboseManager.SetLevel(sessionKey, session.VerboseLevel(level))
	return true
}

// ============================================================================
// AgentProvidable Interface - Thinking Level
// ============================================================================

// GetThinkLevel returns the current reasoning effort level for a session.
func (ap *agentProvidableImpl) GetThinkLevel(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "default"
	}
	// 1) In-memory (fast path)
	if v, ok := ap.al.sessionThinking.Load(sessionKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	// 2) Persisted (survives restarts)
	agent := ap.al.agentForSession(sessionKey)
	if agent != nil && agent.Sessions != nil {
		persisted := agent.Sessions.GetThinkingLevel(sessionKey)
		if persisted != "" {
			// Sync back to in-memory
			ap.al.sessionThinking.Store(sessionKey, persisted)
			return persisted
		}
	}
	return "default"
}

// SetThinkLevel sets the reasoning effort level for a session.
func (ap *agentProvidableImpl) SetThinkLevel(sessionKey string, level string) bool {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return false
	}
	validLevels := map[string]bool{"default": true, "off": true, "low": true, "medium": true, "high": true}
	if !validLevels[level] {
		return false
	}
	if level == "off" || level == "default" {
		ap.al.sessionThinking.Delete(sessionKey)
	} else {
		ap.al.sessionThinking.Store(sessionKey, level)
	}
	// Persist to survive restarts
	agent := ap.al.agentForSession(sessionKey)
	if agent != nil && agent.Sessions != nil {
		persistLevel := level
		if level == "default" {
			persistLevel = "" // "default" means "no override"
		}
		agent.Sessions.SetThinkingLevel(sessionKey, persistLevel)
	}
	return true
}

// ============================================================================
// AgentProvidable Interface - Subagents
// ============================================================================

// GetSubagents lists running subagents.
func (ap *agentProvidableImpl) GetSubagents() string {
	return formatSubagentTaskList(ap.al.toolCoordinator.listRunningSubagentTasks())
}

// GetSessionSubagents returns subagent tasks that belong to a given session.
// It returns both in-memory tasks (from SubagentManagers) and persisted past
// tasks discovered from session storage, so that completed subagents survive
// server restarts.
func (ap *agentProvidableImpl) GetSessionSubagents(sessionKey string) []channels.SubagentTaskInfo {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	var result []channels.SubagentTaskInfo

	// Track which task IDs we already have from in-memory managers
	seen := make(map[string]bool)

	// 1. Collect in-memory tasks (rich data: label, agentID, live status)
	for _, manager := range ap.al.toolCoordinator.GetSubagents() {
		for _, task := range manager.ListTasks() {
			resolvedOrigin := ap.al.ResolveSessionKey(task.OriginSessionKey)
			if resolvedOrigin != resolvedKey {
				continue
			}
			seen[task.ID] = true
			result = append(result, channels.SubagentTaskInfo{
				TaskID:     task.ID,
				SessionKey: resolvedKey + ":" + task.ID,
				Label:      task.Label,
				AgentID:    task.AgentID,
				Status:     task.Status,
				Summary:    task.Summary,
				Created:    task.Created,
				Updated:    task.Updated,
				Iterations: task.Iterations,
			})
		}
	}

	// 2. Scan all agents' session storage for persisted past subagent sessions.
	//    These are subagents that ran in previous server sessions and whose
	//    in-memory tasks are gone, but whose history was saved to disk.
	for _, agentID := range ap.al.registry.ListAgentIDs() {
		agent, ok := ap.al.registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Sessions == nil {
			continue
		}
		for _, past := range agent.Sessions.FindSubagentSessions(resolvedKey) {
			if seen[past.TaskID] {
				continue // in-memory version takes precedence
			}
			seen[past.TaskID] = true

			// Determine a human-readable summary
			summary := past.Summary

			result = append(result, channels.SubagentTaskInfo{
				TaskID:     past.TaskID,
				SessionKey: past.Key,
				Label:      past.Name, // session Name doubles as label fallback
				AgentID:    agentID,   // owning agent of the session storage
				Status:     "completed",
				Summary:    summary,
				Created:    past.Created.UnixMilli(),
				Updated:    past.Updated.UnixMilli(),
				Iterations: past.Iterations,
			})
		}
	}

	return result
}

// ============================================================================
// AgentProvidable Interface - Session Management
// ============================================================================

// ClearSession starts a fresh conversation for the current chat.
// It preserves the selected agent while switching the chat to a new empty session.
func (ap *agentProvidableImpl) ClearSession(sessionKey string) string {
	baseSessionKey := strings.TrimSpace(sessionKey)
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured"
	}
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = ap.al.cfg().Agents.Defaults.Model
	}
	if baseSessionKey == "" {
		baseSessionKey = sessionKey
	}
	ap.al.startFreshConversation(baseSessionKey, agent.ID, agentModel)
	return "🔄 New conversation started. Context refreshed from AGENT.md, SOUL.md, USER.md, IDENTITY.md, and MEMORY.md."
}

// GetName returns the name of a session.
func (ap *agentProvidableImpl) GetName(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return ""
	}
	return agent.Sessions.GetName(sessionKey)
}

// GetUpdated returns the timestamp of last update for a session.
func (ap *agentProvidableImpl) GetUpdated(sessionKey string) time.Time {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return time.Time{}
	}
	return agent.Sessions.GetUpdated(sessionKey)
}

// GetCreated returns the creation timestamp of a session.
func (ap *agentProvidableImpl) GetCreated(sessionKey string) time.Time {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return time.Time{}
	}
	return agent.Sessions.GetCreated(sessionKey)
}

// SetName sets the name of a session.
func (ap *agentProvidableImpl) SetName(sessionKey string, name string) error {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return fmt.Errorf("no agent available for session")
	}
	return agent.Sessions.SetName(sessionKey, name)
}

// GetSessionSummary returns the summary of a session.
func (ap *agentProvidableImpl) GetSessionSummary(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return ""
	}
	return agent.Sessions.GetSummary(sessionKey)
}

// ============================================================================
// AgentProvidable Interface - Session Key Resolution
// ============================================================================

// ResolveSessionKey resolves the alias of session_key if exists.
func (ap *agentProvidableImpl) ResolveSessionKey(sessionKey string) string {
	return ap.al.ResolveSessionKey(sessionKey)
}

// GetSubagentParentSessionKey returns the parent session key for a subagent.
func (ap *agentProvidableImpl) GetSubagentParentSessionKey(sessionKey string) string {
	return ap.al.GetSubagentParentSessionKey(sessionKey)
}

// ============================================================================
// AgentProvidable Interface - Processing Status
// ============================================================================

// IsSessionProcessing returns true if there is an active LLM processing loop for the session.
func (ap *agentProvidableImpl) IsSessionProcessing(sessionKey string) bool {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if ap.al.sessionManager.IsSessionProcessing(sessionKey) {
		return true
	}
	return ap.al.isGoalLoopActive(sessionKey)
}

// ============================================================================
// AgentProvidable Interface - Token Counts
// ============================================================================

// GetTokenCounts returns the input/output token counts and context window for a session.
func (ap *agentProvidableImpl) GetTokenCounts(sessionKey string) (inputTokens, outputTokens int, contextWindow int) {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0, 0, 0
	}
	inputTokens, outputTokens = agent.Sessions.GetTokenCounts(resolvedSessionKey)
	contextWindow = ap.GetSessionContextWindow(sessionKey)
	return
}

// GetCompactionCount returns the number of compactions performed on a session.
func (ap *agentProvidableImpl) GetCompactionCount(sessionKey string) int {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0
	}
	session := agent.Sessions.GetOrCreate(resolvedSessionKey)
	if session == nil {
		return 0
	}
	return session.CompactionCount
}

// GetSessionContextWindow returns the context window for a session.
func (ap *agentProvidableImpl) GetSessionContextWindow(sessionKey string) int {
	return ap.al.getSessionContextWindow(sessionKey)
}

// GetCurrentContextUsage returns the actual current context size.
func (ap *agentProvidableImpl) GetCurrentContextUsage(sessionKey string) (currentTokens, contextWindow int) {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0, 0
	}

	// Get history and estimate its token count
	history := agent.Sessions.GetHistoryView(resolvedSessionKey)
	historyTokens := ap.al.sessionManager.EstimateTokens(history)

	// Get summary and estimate its token count
	summary := agent.Sessions.GetSummary(resolvedSessionKey)
	summaryTokens := 0
	if summary != "" && !hasSummaryMessage(history, summary) {
		summaryTokens = ap.al.sessionManager.EstimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	// Build system prompt and estimate its token count
	systemPrompt := agent.ContextBuilder.BuildSystemPromptForSession(resolvedSessionKey, "")
	systemTokens := ap.al.sessionManager.EstimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})

	currentTokens = systemTokens + summaryTokens + historyTokens
	contextWindow = ap.GetSessionContextWindow(sessionKey)
	return
}

// ============================================================================
// Public Methods for External Access (Direct Processing)
// ============================================================================

// ResolveRoute computes the unified session key for a channel+peer combination
// using the configured DM scope and identity links via the routing resolver.
func (ap *agentProvidableImpl) ResolveRoute(channel, peerKind, peerID string) string {
	peer := &routing.RoutePeer{
		Kind: peerKind,
		ID:   peerID,
	}
	if peerKind == "" {
		peer.Kind = "direct"
	}

	route := ap.al.registry.ResolveRoute(routing.RouteInput{
		Channel: channel,
		Peer:    peer,
	})
	return route.SessionKey
}

// ProcessDirect processes a message directly without going through the message bus.
func (ap *agentProvidableImpl) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return ap.al.messageProcessor.ProcessDirect(ctx, content, sessionKey)
}

// ProcessDirectWithChannel processes a message directly with channel information.
func (ap *agentProvidableImpl) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	return ap.al.messageProcessor.ProcessDirectWithChannel(ctx, content, sessionKey, channel, chatID)
}

// ProcessHeartbeat processes a heartbeat request without session history.
func (ap *agentProvidableImpl) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return ap.al.messageProcessor.ProcessHeartbeat(ctx, content, channel, chatID)
}

// ListAllSessions returns a summary of every persisted session across all
// agents (including system sessions such as heartbeat and cron).
func (ap *agentProvidableImpl) ListAllSessions() []channels.SessionKindInfo {
	sm := ap.al.SessionManager()
	if sm == nil {
		return []channels.SessionKindInfo{}
	}

	sessions := sm.ListSessions()
	// Get accurate message counts in batch — sessions only in metadata
	// (not loaded in memory) would otherwise report 0 messages, causing
	// them to be hidden in the WebUI sidebar.
	msgCounts := sm.AllMessageCounts()

	result := make([]channels.SessionKindInfo, 0, len(sessions))
	for _, s := range sessions {
		if s == nil {
			continue
		}
		// Use the batch-queried count (accurate for both in-memory and
		// metadata-only sessions). Fall back to in-memory count if the
		// batch query didn't return a result for this key.
		count := msgCounts[s.Key]
		if count == 0 && len(s.Messages) > 0 {
			count = 0
			for _, msg := range s.Messages {
				if msg.Role == "user" || msg.Role == "assistant" {
					if msg.Role == "user" && msg.Content == "" && len(msg.ContentParts) > 0 {
						continue
					}
					count++
				}
			}
		}
		result = append(result, channels.SessionKindInfo{
			Key:          s.Key,
			Name:         s.Name,
			Mode:         s.Mode,
			Kind:         classifySessionKind(s.Key),
			Created:      s.Created,
			Updated:      s.Updated,
			MessageCount: count,
		})
	}
	return result
}

// classifySessionKind derives the session kind from the session key.
//   - "heartbeat"  — heartbeat service runs (key "heartbeat")
//   - "cron"       — global cron job turns (key "cron-<jobID>")
//   - "cron-spawn" — cron jobs that spawn a subagent (key "cron-spawn-<jobID>")
//   - "subagent"   — subagent sessions (key "subagent:..." or ":subagent-N")
//   - "chat"       — everything else (user chats)
func classifySessionKind(sessionKey string) string {
	if sessionKey == "" {
		return "chat"
	}
	if sessionKey == "heartbeat" {
		return "heartbeat"
	}
	if strings.HasPrefix(sessionKey, "cron-spawn-") {
		return "cron-spawn"
	}
	if strings.HasPrefix(sessionKey, "cron-") {
		return "cron"
	}
	if routing.IsSubagentSessionKey(sessionKey) {
		return "subagent"
	}
	if idx := strings.LastIndex(sessionKey, ":subagent-"); idx > 0 {
		return "subagent"
	}
	return "chat"
}

// ============================================================================
// Streaming support — persists assistant message chunks in the session file
// ============================================================================

// AppendAssistantChunk appends a content chunk to the in-progress assistant message.
func (ap *agentProvidableImpl) AppendAssistantChunk(sessionKey, chunk string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.AppendAssistantChunk(resolvedKey, chunk)
}

// AppendReasoningChunk appends a reasoning chunk to the in-progress assistant message.
func (ap *agentProvidableImpl) AppendReasoningChunk(sessionKey, chunk string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.AppendReasoningChunk(resolvedKey, chunk)
}

// FinalizeAssistantMessage marks the in-progress assistant message as complete.
func (ap *agentProvidableImpl) FinalizeAssistantMessage(sessionKey string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.FinalizeAssistantMessage(resolvedKey)
}

// HasStreamedContent returns true if the session has an in-progress streaming message with content.
func (ap *agentProvidableImpl) HasStreamedContent(sessionKey string) bool {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return false
	}
	return agent.Sessions.HasStreamedContent(resolvedKey)
}

// GetInProgressAssistant returns the in-progress assistant message, if any.
func (ap *agentProvidableImpl) GetInProgressAssistant(sessionKey string) *providers.Message {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	return agent.Sessions.GetInProgressAssistant(resolvedKey)
}

// ============================================================================
// AgentProvidable Interface - Background Exec Management
// ============================================================================

// GetBackgroundExecs returns all background processes across all agents.
func (ap *agentProvidableImpl) GetBackgroundExecs(includeCompleted bool) []channels.BackgroundExecInfo {
	if ap.al.toolCoordinator == nil {
		return nil
	}
	infos := ap.al.toolCoordinator.getBackgroundExecs(includeCompleted)
	result := make([]channels.BackgroundExecInfo, len(infos))
	for i, info := range infos {
		result[i] = channels.BackgroundExecInfo{
			ID:         info.ID,
			AgentID:    info.AgentID,
			Command:    info.Command,
			WorkingDir: info.WorkingDir,
			Status:     info.Status,
			StartTime:  info.StartTime,
			EndTime:    info.EndTime,
			ExitCode:   info.ExitCode,
			Elapsed:    info.Elapsed.Milliseconds(),
		}
	}
	return result
}

// GetBackgroundExecOutput returns the output of a background process.
func (ap *agentProvidableImpl) GetBackgroundExecOutput(id string, tail int) (output string, status string, elapsedMs int64, err error) {
	if ap.al.toolCoordinator == nil {
		return "", "", 0, fmt.Errorf("tool coordinator not available")
	}
	output, status, elapsed, err := ap.al.toolCoordinator.getBackgroundExecOutput(id, tail)
	return output, status, elapsed.Milliseconds(), err
}

// StopBackgroundExec stops a running background process.
func (ap *agentProvidableImpl) StopBackgroundExec(id string) error {
	if ap.al.toolCoordinator == nil {
		return fmt.Errorf("tool coordinator not available")
	}
	return ap.al.toolCoordinator.stopBackgroundExec(id)
}

// AllGroupSnapshots returns a GroupSnapshot for every tracked group.
func (ap *agentProvidableImpl) AllGroupSnapshots() []group.GroupSnapshot {
	return ap.al.AllGroupSnapshots()
}
