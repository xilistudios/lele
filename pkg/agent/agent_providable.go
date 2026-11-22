package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	lelecontext "github.com/xilistudios/lele/pkg/context"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/tools"
)

type agentProvidableImpl struct {
	al *AgentLoop
}

func newAgentProvidable(al *AgentLoop) *agentProvidableImpl {
	return &agentProvidableImpl{al: al}
}

func (ap *agentProvidableImpl) GetSessionAgent(sessionKey string) string {
	return ap.al.getSessionAgent(sessionKey)
}

func (ap *agentProvidableImpl) SetSessionAgent(sessionKey, agentID string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	currentAgentID := ap.GetSessionAgent(sessionKey)
	if _, pinned := ap.al.sessionAgentOverride(resolvedKey); pinned && currentAgentID == agentID {
		return
	}

	ap.assignSessionAgent(resolvedKey, currentAgentID, agentID)
}

func (ap *agentProvidableImpl) HintSessionAgent(sessionKey, agentID string) {
	if agentID == "" {
		return
	}
	currentAgentID := ap.GetSessionAgent(sessionKey)
	if currentAgentID == agentID {
		return
	}
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	override, hadOverride := ap.al.sessionModels.Load(resolvedKey)
	ap.assignSessionAgent(resolvedKey, currentAgentID, agentID)
	if hadOverride {
		if model, isString := override.(string); isString && model != "" {
			ap.SetSessionModel(resolvedKey, model)
		}
	}
}

func (ap *agentProvidableImpl) assignSessionAgent(resolvedKey, currentAgentID, agentID string) {
	if currentAgentID != "" {
		oldAgent, oldOk := ap.al.registry.GetAgent(currentAgentID)
		newAgent, newOk := ap.al.registry.GetAgent(agentID)
		if oldOk && newOk && oldAgent != nil && newAgent != nil {
			history := oldAgent.Sessions.GetHistory(resolvedKey)
			summary := oldAgent.Sessions.GetSummary(resolvedKey)
			name := oldAgent.Sessions.GetName(resolvedKey)
			verboseLevel := oldAgent.Sessions.GetVerboseLevel(resolvedKey)
			thinkingLevel := oldAgent.Sessions.GetThinkingLevel(resolvedKey)

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

	ap.al.setSessionAgent(resolvedKey, agentID)
	ap.al.sessionModels.Delete(resolvedKey)

	if agent, ok := ap.al.registry.GetAgent(agentID); ok && agent != nil && agent.Sessions != nil {
		agent.Sessions.SetModel(resolvedKey, "")
	}
}

func (ap *agentProvidableImpl) ListAvailableAgentIDs() []string {
	return ap.al.registry.ListAgentIDs()
}

func (ap *agentProvidableImpl) GetDefaultAgentID() string {
	return ap.al.getDefaultAgentID()
}

func (ap *agentProvidableImpl) GetAgentInfo(agentID string) (channels.AgentBasicInfo, bool) {
	agent, ok := ap.al.registry.GetAgent(agentID)
	if !ok {
		return channels.AgentBasicInfo{}, false
	}
	return channels.AgentBasicInfo{
		ID:             agent.ID,
		Name:           agent.Name,
		Description:    agent.Description,
		Model:          agent.Model,
		Workspace:      agent.Workspace,
		MaxIterations:  agent.MaxIterations,
		MaxTokens:      agent.MaxTokens,
		Temperature:    agent.Temperature,
		Fallbacks:      agent.Fallbacks,
		SkillsFilter:   agent.SkillsFilter,
		Reasoning:      agent.Reasoning,
		SupportsImages: agent.SupportsImages,
		SupportsVideo:  agent.SupportsVideo,
	}, true
}

func (ap *agentProvidableImpl) ListAgentTools(agentID string) ([]channels.AgentToolInfo, bool) {
	agent, ok := ap.al.registry.GetAgent(agentID)
	if !ok || agent == nil || agent.Tools == nil {
		return nil, false
	}

	names := agent.Tools.List()
	infos := make([]channels.AgentToolInfo, 0, len(names))
	for _, name := range names {
		tool, found := agent.Tools.Get(name)
		if !found {
			continue
		}
		infos = append(infos, channels.AgentToolInfo{Name: tool.Name(), Description: tool.Description()})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, true
}

func (ap *agentProvidableImpl) AgentSkills(agentID string) (loader *skills.SkillsLoader, invalidate func(), ok bool) {
	agent, found := ap.al.registry.GetAgent(agentID)
	if !found || agent == nil || agent.ContextBuilder == nil {
		return nil, nil, false
	}
	cb := agent.ContextBuilder
	return cb.SkillsLoader(), cb.InvalidatePromptCache, true
}

func (ap *agentProvidableImpl) MCPPathsFor(agentID string) (mcp.Paths, bool) {
	if ap.al == nil {
		return mcp.Paths{}, false
	}
	return ap.al.MCPPathsFor(agentID)
}

func (ap *agentProvidableImpl) InvalidateHarnessWorkspace(workspace string) {
	if ap.al == nil {
		return
	}
	ap.al.InvalidateHarnessWorkspace(workspace)
}

func (ap *agentProvidableImpl) AddSessionMessage(sessionKey string, msg providers.Message) error {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return fmt.Errorf("no agent found for session: %s", resolvedSessionKey)
	}

	agent.Sessions.AddFullMessage(resolvedSessionKey, msg)
	return nil
}

func (ap *agentProvidableImpl) GetSessionHistory(sessionKey string) []providers.Message {
	return copyMessageSlice(ap.GetHistoryView(sessionKey))
}

func copyMessageSlice(msgs []providers.Message) []providers.Message {
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	return out
}

func (ap *agentProvidableImpl) GetHistoryView(sessionKey string) []providers.Message {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				history := agent.Sessions.GetHistoryView(resolvedSessionKey)
				if len(history) > 0 {
					return history
				}
			}
		}

		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			history := agent.Sessions.GetHistoryView(resolvedSessionKey)
			if len(history) > 0 {
				ap.al.setSubagentSessionAgent(resolvedSessionKey, agent.ID)
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
				ap.al.setSubagentSessionAgent(resolvedSessionKey, agent.ID)
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
				ap.al.setSubagentSessionAgent(resolvedSessionKey, agent.ID)
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

func (ap *agentProvidableImpl) HasMessages(sessionKey string) bool {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				return agent.Sessions.HasMessages(resolvedSessionKey)
			}
		}
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			if agent.Sessions.HasMessages(resolvedSessionKey) {
				ap.al.setSubagentSessionAgent(resolvedSessionKey, agent.ID)
				return true
			}
		}
		return false
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return false
	}
	return agent.Sessions.HasMessages(resolvedSessionKey)
}

func (ap *agentProvidableImpl) GetSessionModel(sessionKey string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return ""
	}
	if model, ok := ap.al.sessionModels.Load(resolvedSessionKey); ok {
		if selected, ok := model.(string); ok && selected != "" {
			return selected
		}
	}
	if agent.Sessions != nil {
		persistedModel := agent.Sessions.GetModel(resolvedSessionKey)
		if persistedModel != "" {
			return persistedModel
		}
	}
	return agent.Model
}

func (ap *agentProvidableImpl) GetSessionMode(sessionKey string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil || agent.Sessions == nil {
		return ""
	}
	return agent.Sessions.GetMode(resolvedSessionKey)
}

func (ap *agentProvidableImpl) SetSessionMode(sessionKey, mode string) error {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("no agent available for session")
	}
	return agent.Sessions.SetMode(resolvedSessionKey, mode)
}

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

func (ap *agentProvidableImpl) GetSessionModelSupportsVideo(sessionKey string) bool {
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
	return getSupportsVideo(cfg, model, providerName)
}

func (ap *agentProvidableImpl) SetSessionModel(sessionKey, model string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	if resolvedSessionKey == "" {
		return ""
	}
	next := ap.al.cfg().Providers.ResolveModelAlias(model, ap.al.cfg().Agents.Defaults.Provider)
	ap.al.sessionModels.Store(resolvedSessionKey, next)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent != nil && agent.Sessions != nil {
		agent.Sessions.SetModel(resolvedSessionKey, next)
	}
	return next
}

func (ap *agentProvidableImpl) SetSessionFolder(sessionKey, folder string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	if resolvedSessionKey == "" {
		return ""
	}
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent != nil && agent.Sessions != nil {
		if err := agent.Sessions.SetFolder(resolvedSessionKey, folder); err != nil {
			logger.WarnCF("agent", "Failed to persist session folder", map[string]interface{}{
				"session_key": resolvedSessionKey,
				"folder":      folder,
				"error":       err.Error(),
			})
		}
	}
	return folder
}

func (ap *agentProvidableImpl) GetSessionFolder(sessionKey string) string {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	if resolvedSessionKey == "" {
		return ""
	}
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil || agent.Sessions == nil {
		return ""
	}
	return agent.Sessions.GetFolder(resolvedSessionKey)
}

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

func (ap *agentProvidableImpl) GetConfigSnapshot() *config.Config {
	return ap.al.cfg()
}

func (ap *agentProvidableImpl) GetStatus(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}
	return ap.al.messageProcessor.formatStatusResponse(agent, sessionKey, "telegram")
}

func (ap *agentProvidableImpl) StopAgent(sessionKey string) string {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	logger.InfoCF("agent", "StopAgent called", map[string]interface{}{
		"session_key":   sessionKey,
		"resolved_key":  resolvedKey,
		"is_processing": ap.al.sessionManager.IsSessionProcessing(resolvedKey),
	})

	subagentCount, groupCount, procCount := 0, 0, 0
	if ap.al.toolCoordinator != nil {
		subagentCount, groupCount, procCount = ap.al.toolCoordinator.cancelSessionTree(resolvedKey)
	}
	cancelled := ap.al.cancelSession(resolvedKey)
	logger.InfoCF("agent", "StopAgent completed", map[string]interface{}{
		"session_key":    resolvedKey,
		"cancelled":      cancelled,
		"subagent_count": subagentCount,
		"group_count":    groupCount,
		"process_count":  procCount,
	})

	return formatStopAgentResponse(subagentCount, groupCount, procCount)
}

func formatStopAgentResponse(subagentCount, groupCount, procCount int) string {
	var parts []string
	if subagentCount > 0 {
		parts = append(parts, fmt.Sprintf("%d subagente(s)", subagentCount))
	}
	if groupCount > 0 {
		parts = append(parts, fmt.Sprintf("%d grupo(s)", groupCount))
	}
	if procCount > 0 {
		parts = append(parts, fmt.Sprintf("%d proceso(s) en segundo plano", procCount))
	}
	if len(parts) == 0 {
		return "⏹️ Agente detenido."
	}
	return fmt.Sprintf("⏹️ Agente detenido (incluye %s).", strings.Join(parts, " y "))
}

func (ap *agentProvidableImpl) CompactSession(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}

	if agent.Sessions.GetTotalMessageCount(sessionKey) <= 4 {
		return "📭 Not enough messages to compact (need 5+)."
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	unregister := ap.al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
	defer unregister()
	_ = ctx

	stats := ap.al.sessionManager.summarizeSession(agent, sessionKey)
	if stats == nil {
		return "❌ Compaction failed or nothing to compact."
	}

	return fmt.Sprintf("✅ Compacted session: %d messages → %d messages (%d tokens saved)",
		stats.BeforeMessages, stats.AfterMessages, stats.SavedTokens)
}

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

func (ap *agentProvidableImpl) GetVerboseLevel(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "off"
	}
	return string(ap.al.verboseManager.GetLevel(sessionKey))
}

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

func (ap *agentProvidableImpl) GetThinkLevel(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "default"
	}
	if v, ok := ap.al.sessionThinking.Load(sessionKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	agent := ap.al.agentForSession(sessionKey)
	if agent != nil && agent.Sessions != nil {
		persisted := agent.Sessions.GetThinkingLevel(sessionKey)
		if persisted != "" {
			ap.al.sessionThinking.Store(sessionKey, persisted)
			return persisted
		}
	}
	return "default"
}

func (ap *agentProvidableImpl) GetEffectiveThinkLevel(sessionKey string) string {
	level := ap.GetThinkLevel(sessionKey)
	if level != "default" {
		return level
	}
	if agent := ap.al.agentForSession(ap.al.ResolveSessionKey(sessionKey)); agent != nil && agent.ThinkingLevel != "" {
		return agent.ThinkingLevel
	}
	return "default"
}

func (ap *agentProvidableImpl) SetThinkLevel(sessionKey string, level string) bool {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if sessionKey == "" {
		return false
	}
	if level == "" {
		return false
	}
	normalized, ok := config.NormalizeThinkingLevel(level)
	if !ok {
		return false
	}
	if normalized == "" || normalized == "off" {
		ap.al.sessionThinking.Delete(sessionKey)
	} else {
		ap.al.sessionThinking.Store(sessionKey, normalized)
	}
	agent := ap.al.agentForSession(sessionKey)
	if agent != nil && agent.Sessions != nil {
		agent.Sessions.SetThinkingLevel(sessionKey, normalized)
	}
	return true
}

func (ap *agentProvidableImpl) GetSubagents() string {
	return formatSubagentTaskList(ap.al.toolCoordinator.listRunningSubagentTasks())
}

func (ap *agentProvidableImpl) GetSessionSubagents(sessionKey string) []channels.SubagentTaskInfo {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	var result []channels.SubagentTaskInfo

	seen := make(map[string]bool)

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

	for _, agentID := range ap.al.registry.ListAgentIDs() {
		agent, ok := ap.al.registry.GetAgent(agentID)
		if !ok || agent == nil || agent.Sessions == nil {
			continue
		}
		for _, past := range agent.Sessions.FindSubagentSessions(resolvedKey) {
			if seen[past.TaskID] {
				continue
			}
			seen[past.TaskID] = true

			summary := past.Summary

			status := past.Status
			if status == "" {
				status = tools.SubagentStatusCompleted
			}

			executor := past.AgentID
			if executor == "" {
				executor = ap.al.subagentSessionPin(past.Key)
			}
			if executor == "" {
				executor = agentID
			}

			result = append(result, channels.SubagentTaskInfo{
				TaskID:     past.TaskID,
				SessionKey: past.Key,
				Label:      past.Name,
				AgentID:    executor,
				Status:     status,
				Summary:    summary,
				Created:    past.Created.UnixMilli(),
				Updated:    past.Updated.UnixMilli(),
				Iterations: past.Iterations,
			})
		}
	}

	return result
}

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
	return lelecontext.ContextRefreshedMessage
}

func (ap *agentProvidableImpl) GetName(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return ""
	}
	return agent.Sessions.GetName(sessionKey)
}

func (ap *agentProvidableImpl) GetUpdated(sessionKey string) time.Time {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return time.Time{}
	}
	return agent.Sessions.GetUpdated(sessionKey)
}

func (ap *agentProvidableImpl) GetCreated(sessionKey string) time.Time {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return time.Time{}
	}
	return agent.Sessions.GetCreated(sessionKey)
}

func (ap *agentProvidableImpl) SetName(sessionKey string, name string) error {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return fmt.Errorf("no agent available for session")
	}
	return agent.Sessions.SetName(sessionKey, name)
}

func (ap *agentProvidableImpl) GetSessionSummary(sessionKey string) string {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(sessionKey)
	if agent == nil {
		return ""
	}
	return agent.Sessions.GetSummary(sessionKey)
}

func (ap *agentProvidableImpl) ResolveSessionKey(sessionKey string) string {
	return ap.al.ResolveSessionKey(sessionKey)
}

func (ap *agentProvidableImpl) GetSubagentParentSessionKey(sessionKey string) string {
	return ap.al.GetSubagentParentSessionKey(sessionKey)
}

func (ap *agentProvidableImpl) IsSessionProcessing(sessionKey string) bool {
	sessionKey = ap.al.ResolveSessionKey(sessionKey)
	if ap.al.sessionManager.IsSessionProcessing(sessionKey) {
		return true
	}
	return ap.al.isGoalLoopActive(sessionKey)
}

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

func (ap *agentProvidableImpl) GetSessionContextWindow(sessionKey string) int {
	return ap.al.getSessionContextWindow(sessionKey)
}

func (ap *agentProvidableImpl) GetCurrentContextUsage(sessionKey string) (currentTokens, contextWindow int) {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 0, 0
	}

	history := agent.Sessions.GetHistoryView(resolvedSessionKey)
	historyTokens := ap.al.sessionManager.EstimateTokens(history)

	summary := agent.Sessions.GetSummary(resolvedSessionKey)
	summaryTokens := 0
	if summary != "" && !hasSummaryMessage(history, summary) {
		summaryTokens = ap.al.sessionManager.EstimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	systemPrompt := agent.ContextBuilder.BuildSystemPromptForSessionWithFolder(resolvedSessionKey, "")
	systemTokens := ap.al.sessionManager.EstimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})

	currentTokens = systemTokens + summaryTokens + historyTokens
	contextWindow = ap.GetSessionContextWindow(sessionKey)
	return
}

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

func (ap *agentProvidableImpl) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return ap.al.messageProcessor.ProcessDirect(ctx, content, sessionKey)
}

func (ap *agentProvidableImpl) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	return ap.al.messageProcessor.ProcessDirectWithChannel(ctx, content, sessionKey, channel, chatID)
}

func (ap *agentProvidableImpl) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	return ap.al.messageProcessor.ProcessHeartbeat(ctx, content, channel, chatID)
}

func (ap *agentProvidableImpl) ListAllSessions() []channels.SessionKindInfo {
	sm := ap.al.SessionManager()
	if sm == nil {
		return []channels.SessionKindInfo{}
	}

	entries := sm.ListSessionIndex()

	needsStore := false
	for _, e := range entries {
		if !e.Resident {
			needsStore = true
			break
		}
	}
	var withMessages map[string]bool
	if needsStore {
		withMessages = sm.SessionKeysWithMessages()
	}

	result := make([]channels.SessionKindInfo, 0, len(entries))
	for _, e := range entries {
		has := e.HasMessages
		if !e.Resident && withMessages != nil {
			has = withMessages[e.Key]
		}
		result = append(result, channels.SessionKindInfo{
			Key:         e.Key,
			Name:        e.Name,
			Mode:        e.Mode,
			Kind:        classifySessionKind(e.Key),
			Folder:      e.Folder,
			Created:     e.Created,
			Updated:     e.Updated,
			Resident:    e.Resident,
			HasMessages: has,
		})
	}
	return result
}

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
	if strings.Contains(sessionKey, ":cron-") && strings.Contains(sessionKey, ":subagent-") {
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

func (ap *agentProvidableImpl) AppendAssistantChunk(sessionKey, chunk string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.AppendAssistantChunk(resolvedKey, chunk)
}

func (ap *agentProvidableImpl) AppendReasoningChunk(sessionKey, chunk string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.AppendReasoningChunk(resolvedKey, chunk)
}

func (ap *agentProvidableImpl) FinalizeAssistantMessage(sessionKey string) {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.FinalizeAssistantMessage(resolvedKey)
}

func (ap *agentProvidableImpl) AttachFilesToLastAssistant(sessionKey string, attachments []providers.MessageAttachment) {
	if len(attachments) == 0 {
		return
	}
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return
	}
	agent.Sessions.AttachFilesToLastAssistant(resolvedKey, attachments)
}

func (ap *agentProvidableImpl) HasStreamedContent(sessionKey string) bool {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return false
	}
	return agent.Sessions.HasStreamedContent(resolvedKey)
}

func (ap *agentProvidableImpl) GetInProgressAssistant(sessionKey string) *providers.Message {
	resolvedKey := ap.al.ResolveSessionKey(sessionKey)
	agent := ap.al.agentForSession(resolvedKey)
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	return agent.Sessions.GetInProgressAssistant(resolvedKey)
}

func (ap *agentProvidableImpl) GetInProgressTool(sessionKey string) *session.InProgressTool {
	return ap.al.GetInProgressTool(sessionKey)
}

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

func (ap *agentProvidableImpl) GetBackgroundExecOutput(id string, tail int) (output string, status string, elapsedMs int64, err error) {
	if ap.al.toolCoordinator == nil {
		return "", "", 0, fmt.Errorf("tool coordinator not available")
	}
	output, status, elapsed, err := ap.al.toolCoordinator.getBackgroundExecOutput(id, tail)
	return output, status, elapsed.Milliseconds(), err
}

func (ap *agentProvidableImpl) StopBackgroundExec(id string) error {
	if ap.al.toolCoordinator == nil {
		return fmt.Errorf("tool coordinator not available")
	}
	return ap.al.toolCoordinator.stopBackgroundExec(id)
}

func (ap *agentProvidableImpl) AllGroupSnapshots() []group.GroupSnapshot {
	return ap.al.AllGroupSnapshots()
}

func (ap *agentProvidableImpl) GroupSnapshotsForSession(sessionKey string) []group.GroupSnapshot {
	return ap.al.GroupSnapshotsForSession(sessionKey)
}

func (ap *agentProvidableImpl) LoadEvictedMessagesPage(sessionKey string, before, after, limit int) *session.EvictedMessagesPage {
	resolvedSessionKey := ap.al.ResolveSessionKey(sessionKey)

	if routing.IsSubagentSessionKey(resolvedSessionKey) {
		if agentID, ok := ap.al.subagentSessionAgent.Load(resolvedSessionKey); ok {
			if agent, ok := ap.al.registry.GetAgent(agentID.(string)); ok && agent != nil {
				return agent.Sessions.LoadMessagesWindow(resolvedSessionKey, before, after, limit)
			}
		}
		for _, agentID := range ap.al.registry.ListAgentIDs() {
			agent, ok := ap.al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			if page := agent.Sessions.LoadMessagesWindow(resolvedSessionKey, before, after, limit); page != nil {
				ap.al.setSubagentSessionAgent(resolvedSessionKey, agent.ID)
				return page
			}
		}
		return nil
	}

	agent := ap.al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return nil
	}
	return agent.Sessions.LoadMessagesWindow(resolvedSessionKey, before, after, limit)
}
