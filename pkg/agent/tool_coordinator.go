package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

type toolCoordinator interface {
	updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string)
	stopAllSubagents() int
	stopSessionSubagents(sessionKey string) int
	cancelSessionTree(sessionKey string) (subagents, groups, procs int)
	cancelRemovedSubagents(liveAgentIDs []string) int
	cancelSession(sessionKey string)
	listRunningSubagentTasks() []*tools.SubagentTask
	snapshotRunningSubagents(sessionKey string) []*tools.SubagentTask
	getSubagentTask(taskID string) (*tools.SubagentTask, bool)
	claimSubagentResultForInjection(taskID string) bool
	stopSubagentTask(taskID string) bool
	continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error)
	renderSubagentLedgerDigest(agentID, sessionKey string) string
	GetStartupInfo() map[string]interface{}
	RegisterTool(tool tools.Tool)
	syncMCPTools()
	closeMCPManagers() error
	sessionTools(agent *AgentInstance, sessionKey string) *tools.ToolRegistry
	dropSessionTools(sessionKey string)
	GetSubagents() map[string]*tools.SubagentManager
	getBackgroundExecs(includeCompleted bool) []BackgroundExecInfo
	getBackgroundExecOutput(id string, tail int) (output string, status string, elapsed time.Duration, err error)
	stopBackgroundExec(id string) error
}

type toolCoordinatorImpl struct {
	al          *AgentLoop
	subagents   map[string]*tools.SubagentManager
	bgManagers  map[string]*tools.BackgroundProcessManager
	mcpManagers *mcpManagerSet
	registry    *AgentRegistry
	bus         *bus.MessageBus
	approvalMgr *channels.ApprovalManager
}

func newToolCoordinator(al *AgentLoop) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   make(map[string]*tools.SubagentManager),
		bgManagers:  make(map[string]*tools.BackgroundProcessManager),
		mcpManagers: newMCPManagerSet(),
		registry:    al.registry,
		bus:         al.bus,
		approvalMgr: al.approvalManager,
	}
}

func newToolCoordinatorWithSubagents(al *AgentLoop, subagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager, mcpManagers *mcpManagerSet) *toolCoordinatorImpl {
	if mcpManagers == nil {
		mcpManagers = newMCPManagerSet()
	}
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   subagents,
		bgManagers:  bgManagers,
		mcpManagers: mcpManagers,
		registry:    al.registry,
		bus:         al.bus,
		approvalMgr: al.approvalManager,
	}
}

func (tc *toolCoordinatorImpl) updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string) {
	registry := tc.sessionTools(agent, sessionKey)
	if registry == nil {
		return
	}
	for _, name := range registry.List() {
		tool, ok := registry.Get(name)
		if !ok {
			continue
		}

		if st, ok := tool.(tools.SessionAwareTool); ok {
			st.SetSessionContext(channel, chatID, sessionKey)
		} else if ct, ok := tool.(tools.ContextualTool); ok {
			ct.SetContext(channel, chatID)
		}

		if et, ok := tool.(*tools.ExecTool); ok {
			verboseLevel := tc.al.verboseManager.GetLevel(sessionKey)
			et.SetVerbose(verboseLevel)
			et.SetFeedbackCallback(func(ch, cid, msg string) {
				tc.al.bus.PublishOutbound(bus.OutboundMessage{
					Channel:        ch,
					ChatID:         cid,
					Content:        msg,
					IsIntermediate: true,
				})
			})
		}
	}
}

func (tc *toolCoordinatorImpl) stopAllSubagents() int {
	totalStopped := 0
	for _, manager := range tc.subagents {
		if manager != nil {
			stopped := manager.StopAll()
			totalStopped += stopped
		}
	}
	return totalStopped
}

func (tc *toolCoordinatorImpl) sessionCancellationKeys(sessionKey string) []string {
	if sessionKey == "" {
		return nil
	}
	keys := []string{sessionKey}
	if resolved := tc.al.ResolveSessionKey(sessionKey); resolved != sessionKey {
		keys = append(keys, resolved)
	}
	return keys
}

func (tc *toolCoordinatorImpl) stopSessionSubagents(sessionKey string) int {
	keys := tc.sessionCancellationKeys(sessionKey)
	totalStopped := 0
	for _, manager := range tc.subagents {
		if manager != nil {
			for _, task := range manager.ListTasks() {
				if tools.TaskBelongsToSession(task, keys) {
					if manager.StopTask(task.ID) {
						totalStopped++
					}
					manager.SealResult(task.ID)
				}
			}
		}
	}
	return totalStopped
}

func (tc *toolCoordinatorImpl) cancelSessionTree(sessionKey string) (subagents, groups, procs int) {
	if sessionKey == "" {
		return 0, 0, 0
	}

	subagents = tc.stopSessionSubagents(sessionKey)
	keys := tc.sessionCancellationKeys(sessionKey)

	if gm := tc.al.GroupManager(); gm != nil {
		for _, k := range keys {
			groups += gm.StopByOrigin("", k)
		}
	}

	for _, mgr := range tc.bgManagers {
		if mgr == nil {
			continue
		}
		for _, k := range keys {
			procs += mgr.StopForSession(k)
		}
	}

	return subagents, groups, procs
}

func (tc *toolCoordinatorImpl) cancelRemovedSubagents(liveAgentIDs []string) int {
	live := make(map[string]bool, len(liveAgentIDs))
	for _, id := range liveAgentIDs {
		live[id] = true
	}

	stopped := 0
	for agentID, manager := range tc.subagents {
		if live[agentID] {
			continue
		}
		if manager != nil {
			stopped += manager.StopAll()
			manager.StopRetentionCleanup()
		}
		if tc.al != nil && tc.al.groupManager != nil {
			tc.al.groupManager.StopByAgent(agentID)
		}
		if bgm := tc.bgManagers[agentID]; bgm != nil {
			bgm.StopAll()
		}
		delete(tc.subagents, agentID)
		delete(tc.bgManagers, agentID)
	}
	return stopped
}

func (tc *toolCoordinatorImpl) cancelSession(sessionKey string) {
	tc.al.cancelSession(sessionKey)
}

func (tc *toolCoordinatorImpl) listRunningSubagentTasks() []*tools.SubagentTask {
	tasks := make([]*tools.SubagentTask, 0)
	for _, manager := range tc.subagents {
		for _, task := range manager.ListTasks() {
			if task.Status == tools.SubagentStatusRunning || task.Status == tools.SubagentStatusNeedsContext {
				tasks = append(tasks, task)
			}
		}
	}
	return tasks
}

func (tc *toolCoordinatorImpl) getSubagentTask(taskID string) (*tools.SubagentTask, bool) {
	for _, manager := range tc.subagents {
		if task, ok := manager.GetTask(taskID); ok {
			return task, true
		}
	}
	return nil, false
}

func (tc *toolCoordinatorImpl) claimSubagentResultForInjection(taskID string) bool {
	for _, manager := range tc.subagents {
		if _, ok := manager.GetTask(taskID); ok {
			return manager.ClaimResultForInjection(taskID)
		}
	}
	return false
}

func (tc *toolCoordinatorImpl) stopSubagentTask(taskID string) bool {
	for _, manager := range tc.subagents {
		if manager.StopTask(taskID) {
			return true
		}
	}
	return false
}

func (tc *toolCoordinatorImpl) continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error) {
	for _, manager := range tc.subagents {
		task, ok := manager.GetTask(taskID)
		if !ok {
			continue
		}

		callback := func(callbackCtx context.Context, result *tools.ToolResult) {
			publishSubagentAsyncResult(tc.al, sessionKey, task.OriginChannel, task.OriginChatID, task.ID, result)
		}

		return manager.ContinueTask(ctx, taskID, guidance, callback)
	}

	return "", fmt.Errorf("subagent task not found: %s", taskID)
}

const subagentLedgerDigestRows = 8

const subagentLedgerDigestFieldBytes = 200

const subagentLedgerDigestHeader = "## Subagent Tasks\n\n" +
	"Subagent tasks spawned from this session (newest first). " +
	"This ledger survives context compaction: finished tasks stay listed here " +
	"even when the original tool results are no longer in context."

const subagentLedgerDigestHint = "Use `list_active_subagents` (status/limit/offset) to page through the full " +
	"history, and `wait_for_subagent` for the full result of a task that is still alive."

func (tc *toolCoordinatorImpl) renderSubagentLedgerDigest(agentID, sessionKey string) string {
	if tc == nil || sessionKey == "" {
		return ""
	}

	sm := tc.subagents[agentID]
	if sm == nil {
		return ""
	}

	entries := sm.LedgerEntries(tc.subagentLedgerScopeKeys(sessionKey))
	if len(entries) == 0 {
		return ""
	}

	rows := entries
	if len(rows) > subagentLedgerDigestRows {
		rows = rows[:subagentLedgerDigestRows]
	}

	var b strings.Builder
	b.WriteString(subagentLedgerDigestHeader)
	b.WriteString("\n\n")
	for _, entry := range rows {
		b.WriteString(renderSubagentLedgerDigestRow(entry, tc.subagentLedgerExecutor(entry)))
		b.WriteString("\n")
	}
	if overflow := len(entries) - len(rows); overflow > 0 {
		fmt.Fprintf(&b, "… %d older task(s) not shown.\n", overflow)
	}
	b.WriteString("\n")
	b.WriteString(subagentLedgerDigestHint)

	return b.String()
}

func (tc *toolCoordinatorImpl) subagentLedgerScopeKeys(sessionKey string) []string {
	if sessionKey == "" {
		return nil
	}
	keys := make([]string, 0, 3)
	seen := make(map[string]bool, 3)
	add := func(key string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		keys = append(keys, key)
	}

	add(sessionKey)
	if tc.al != nil {
		add(tc.al.ResolveSessionKey(sessionKey))
		for _, origin := range tc.al.sessionOriginsFor(sessionKey) {
			add(origin)
		}
	}
	return keys
}

func (tc *toolCoordinatorImpl) subagentLedgerExecutor(entry tools.SubagentLedgerEntry) string {
	if entry.AgentID != "" {
		return entry.AgentID
	}
	if !entry.Persisted || tc.al == nil || entry.SessionKey == "" {
		return ""
	}
	return tc.al.subagentSessionPin(entry.SessionKey)
}

func renderSubagentLedgerDigestRow(entry tools.SubagentLedgerEntry, agentID string) string {
	var b strings.Builder
	status := entry.Status
	if status == "" {
		status = tools.SubagentStatusUnknown
	}
	fmt.Fprintf(&b, "- %s [%s]", entry.ID, status)
	if entry.Persisted {
		b.WriteString(" (persisted)")
	}
	if agentID != "" {
		b.WriteString(" ")
		b.WriteString(agentID)
	}
	if label := tools.SanitizeLedgerText(entry.Label, subagentLedgerDigestFieldBytes); label != "" {
		fmt.Fprintf(&b, " %q", label)
	}
	if !entry.Persisted {
		if summary := tools.SanitizeLedgerText(entry.Summary, subagentLedgerDigestFieldBytes); summary != "" {
			b.WriteString(": ")
			b.WriteString(summary)
		}
	}
	return b.String()
}

func (tc *toolCoordinatorImpl) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	agent := tc.al.registry.GetDefaultAgent()
	if agent == nil {
		return info
	}

	toolsList := agent.Tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(toolsList),
		"names": toolsList,
	}

	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	info["agents"] = map[string]interface{}{
		"count": len(tc.al.registry.ListAgentIDs()),
		"ids":   tc.al.registry.ListAgentIDs(),
	}

	return info
}

func (tc *toolCoordinatorImpl) RegisterTool(tool tools.Tool) {
	cfg := tc.al.cfg()
	name := tool.Name()
	for _, agentID := range tc.al.registry.ListAgentIDs() {
		agent, ok := tc.al.registry.GetAgent(agentID)
		if !ok {
			continue
		}
		if !allowlistKeeps(agentConfigForID(cfg, agentID), name) {
			continue
		}
		agent.Tools.Register(tool)
	}
}

func (tc *toolCoordinatorImpl) GetSubagents() map[string]*tools.SubagentManager {
	return tc.subagents
}

func (tc *toolCoordinatorImpl) GetBgManagers() map[string]*tools.BackgroundProcessManager {
	return tc.bgManagers
}

type BackgroundExecInfo struct {
	ID         string
	AgentID    string
	Command    string
	WorkingDir string
	Status     string
	StartTime  time.Time
	EndTime    *time.Time
	ExitCode   int
	Elapsed    time.Duration
}

func (tc *toolCoordinatorImpl) getBackgroundExecs(includeCompleted bool) []BackgroundExecInfo {
	var result []BackgroundExecInfo
	for agentID, mgr := range tc.bgManagers {
		var procs []*tools.BackgroundProcess
		if includeCompleted {
			procs = mgr.List()
		} else {
			procs = mgr.ListRunning()
		}
		for _, p := range procs {
			result = append(result, BackgroundExecInfo{
				ID:         p.ID,
				AgentID:    agentID,
				Command:    p.Command,
				WorkingDir: p.WorkingDir,
				Status:     p.Status,
				StartTime:  p.StartTime,
				EndTime:    p.EndTime,
				ExitCode:   p.ExitCode,
				Elapsed:    p.Elapsed(),
			})
		}
	}
	return result
}

func (tc *toolCoordinatorImpl) getBackgroundExecOutput(id string, tail int) (output string, status string, elapsed time.Duration, err error) {
	for _, mgr := range tc.bgManagers {
		if p, ok := mgr.Get(id); ok {
			if tail > 0 {
				output = p.OutputTail(tail)
			} else {
				output = p.Output()
			}
			return output, p.Status, p.Elapsed(), nil
		}
	}
	return "", "", 0, fmt.Errorf("background process not found: %s", id)
}

func (tc *toolCoordinatorImpl) stopBackgroundExec(id string) error {
	for _, mgr := range tc.bgManagers {
		if p, ok := mgr.Get(id); ok {
			if p.Status != tools.BgExecStatusRunning {
				return fmt.Errorf("process %s is not running (status: %s)", id, p.Status)
			}
			if !mgr.Stop(id) {
				return fmt.Errorf("failed to stop process %s", id)
			}
			return nil
		}
	}
	return fmt.Errorf("background process not found: %s", id)
}

func agentConfigForID(cfg *config.Config, agentID string) *config.AgentConfig {
	if cfg == nil {
		return nil
	}
	want := routing.NormalizeAgentID(agentID)
	for i := range cfg.Agents.List {
		if routing.NormalizeAgentID(cfg.Agents.List[i].ID) == want {
			return &cfg.Agents.List[i]
		}
	}
	return nil
}

func allowlistKeeps(agentCfg *config.AgentConfig, name string) bool {
	if agentCfg == nil || len(agentCfg.Tools) == 0 {
		return true
	}
	known := false
	for _, allowed := range agentCfg.Tools {
		trimmed := strings.TrimSpace(allowed)
		if trimmed == "" {
			continue
		}
		known = true
		if trimmed == name {
			return true
		}
	}
	return !known
}

func registerSharedToolsForAgent(agent *AgentInstance, cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, agentID string, subagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) *tools.SubagentManager {
	if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
		PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
		SearXNGEnabled:       cfg.Tools.Web.SearXNG.Enabled,
		SearXNGInstanceURL:   cfg.Tools.Web.SearXNG.InstanceURL,
		SearXNGCategories:    cfg.Tools.Web.SearXNG.Categories,
		SearXNGLanguage:      cfg.Tools.Web.SearXNG.Language,
		SearXNGSafeSearch:    cfg.Tools.Web.SearXNG.SafeSearch,
		SearXNGMaxResults:    cfg.Tools.Web.SearXNG.MaxResults,
	}); searchTool != nil {
		agent.Tools.Register(searchTool)
	}
	agent.Tools.Register(tools.NewWebFetchTool(50000))

	if keyringSvc != nil {
		agent.Tools.Register(tools.NewSecretTool(keyringSvc))
	}

	agent.Tools.Register(tools.NewI2CTool())
	agent.Tools.Register(tools.NewSPITool())

	sendFileTool := tools.NewSendFileTool()
	sendFileTool.SetWorkspaceRestrictions(agent.Workspace, cfg.Agents.Defaults.RestrictToWorkspace)
	sendFileTool.SetSendCallback(func(channel, chatID string, payload tools.SendFilePayload) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel:     channel,
			ChatID:      chatID,
			Content:     payload.Content,
			Attachments: payload.Attachments,
		})
		return nil
	})
	agent.Tools.Register(sendFileTool)

	bgManager, reusedBg := bgManagers[agentID]
	if !reusedBg || bgManager == nil {
		bgManager = tools.NewBackgroundProcessManager()
		bgManagers[agentID] = bgManager
	}

	execTool := tools.NewExecToolWithConfig(agent.Workspace, cfg.Agents.Defaults.RestrictToWorkspace, cfg)
	execTool.SetBackgroundManager(bgManager)
	if keyringSvc != nil {
		execTool.SetKeyringService(keyringSvc)
	}
	if approvalManager != nil {
		execTool.SetApprovalMode(true)
	}
	agent.Tools.Register(execTool)

	agent.Tools.Register(tools.NewListBackgroundExecsTool(bgManager))
	agent.Tools.Register(tools.NewGetBackgroundExecOutputTool(bgManager))
	agent.Tools.Register(tools.NewStopBackgroundExecTool(bgManager))

	subagentDefaultModel := agent.Model
	subagentDefaultProvider := agent.Provider
	subagentDefaultContextWindow := agent.ContextWindow

	if agent.Subagents != nil && agent.Subagents.Model != nil && strings.TrimSpace(agent.Subagents.Model.Primary) != "" {
		resolvedModel := cfg.Providers.ResolveModelAlias(strings.TrimSpace(agent.Subagents.Model.Primary), cfg.Agents.Defaults.Provider)
		providerName := extractProviderFromModel(resolvedModel, cfg.Agents.Defaults.Provider)
		overrideProvider, err := providers.CreateProviderForCandidate(cfg, providerName)
		if err == nil && overrideProvider != nil {
			subagentDefaultModel = resolvedModel
			subagentDefaultProvider = overrideProvider
			subagentDefaultContextWindow = getContextWindow(cfg, resolvedModel, providerName)
		} else {
			errMsg := "provider is nil"
			if err != nil {
				errMsg = err.Error()
			}
			logger.WarnCF("agent", "Failed to create provider for subagent model override, using parent agent's provider",
				map[string]interface{}{
					"model":    resolvedModel,
					"provider": providerName,
					"error":    errMsg,
				})
		}
	}

	subagentMaxIter := 0
	if cfg.Agents.Defaults.SubagentMaxIterations > 0 {
		subagentMaxIter = cfg.Agents.Defaults.SubagentMaxIterations
	}
	if agent.Subagents != nil && agent.Subagents.MaxIterations > 0 {
		subagentMaxIter = agent.Subagents.MaxIterations
	}
	if subagentMaxIter == 0 && agent.MaxIterations > 0 {
		subagentMaxIter = agent.MaxIterations
	}

	subagentManager, reusedManager := subagents[agentID]
	if !reusedManager || subagentManager == nil {
		subagentManager = tools.NewSubagentManager(subagentDefaultProvider, subagentDefaultModel, agent.Workspace, msgBus, subagentMaxIter)
		subagents[agentID] = subagentManager
	} else {
		subagentManager.SetDefaults(subagentDefaultProvider, subagentDefaultModel, agent.Workspace, subagentMaxIter)
	}
	subagentManager.SetDefaultAgentID(agentID)
	subagentManager.SetCompactionThresholdPercent(cfg.SessionCompactionThresholdPercent())
	subagentManager.SetRedactor(keyring.NewRedactor(keyringSvc))
	subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
	subagentManager.SetThinkingLevel(agent.ThinkingLevel)
	subagentManager.SetModelOverrideResolver(func(model string) (providers.LLMProvider, string, int, error) {
		resolvedModel := cfg.Providers.ResolveModelAlias(model, cfg.Agents.Defaults.Provider)
		providerName := extractProviderFromModel(resolvedModel, cfg.Agents.Defaults.Provider)
		overrideProvider, err := providers.CreateProviderForCandidate(cfg, providerName)
		if err == nil && overrideProvider == nil {
			err = errors.New("provider is nil")
		}
		if err != nil {
			logger.WarnCF("agent", "Failed to create provider for subagent model override",
				map[string]interface{}{
					"model":    model,
					"provider": providerName,
					"error":    err.Error(),
				})
			return nil, "", 0, err
		}
		return overrideProvider, resolvedModel, getContextWindow(cfg, resolvedModel, providerName), nil
	})
	subagentManager.SetVisionChecker(func(model string) bool {
		providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
		return getSupportsImages(cfg, model, providerName)
	})
	subagentManager.SetVideoChecker(func(model string) bool {
		providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
		return getSupportsVideo(cfg, model, providerName)
	})
	if agent.Subagents != nil && agent.Subagents.TimeoutMin > 0 {
		subagentManager.SetTimeout(time.Duration(agent.Subagents.TimeoutMin) * time.Minute)
	} else if cfg.Agents.Defaults.SubagentTimeoutMinutes > 0 {
		subagentManager.SetTimeout(time.Duration(cfg.Agents.Defaults.SubagentTimeoutMinutes) * time.Minute)
	}
	if agent.Subagents != nil && agent.Subagents.MaxConcurrent > 0 {
		subagentManager.SetMaxConcurrent(agent.Subagents.MaxConcurrent)
	} else if cfg.Agents.Defaults.SubagentMaxConcurrent > 0 {
		subagentManager.SetMaxConcurrent(cfg.Agents.Defaults.SubagentMaxConcurrent)
	}
	subagentManager.SetDefaultMaxRetries(cfg.Agents.Defaults.SubagentMaxRetries)
	if mins := cfg.Agents.Defaults.SubagentRetentionMinutes; mins > 0 {
		subagentManager.SetRetentionPeriod(time.Duration(mins) * time.Minute)
	}
	subagentManager.StartRetentionCleanup(tools.DefaultSubagentRetentionSweepInterval)
	subagentManager.SetAgentContextCallback(func(targetAgentID string) tools.AgentContextInfo {
		if targetAgent, ok := registry.GetAgent(targetAgentID); ok {
			return tools.AgentContextInfo{
				Context:       targetAgent.ContextBuilder.GetInitialContext(),
				Workspace:     targetAgent.Workspace,
				Name:          targetAgent.Name,
				Model:         targetAgent.Model,
				Provider:      targetAgent.Provider,
				MaxIterations: targetAgent.MaxIterations,
				MaxTokens:     targetAgent.MaxTokens,
				Temperature:   targetAgent.Temperature,
				ContextWindow: targetAgent.ContextWindow,
				ThinkingLevel: targetAgent.ThinkingLevel,
			}
		}
		return tools.AgentContextInfo{
			Context:       agent.ContextBuilder.GetInitialContext(),
			Workspace:     agent.Workspace,
			Name:          agent.Name,
			Model:         subagentDefaultModel,
			Provider:      subagentDefaultProvider,
			MaxIterations: agent.MaxIterations,
			MaxTokens:     agent.MaxTokens,
			Temperature:   agent.Temperature,
			ContextWindow: subagentDefaultContextWindow,
			ThinkingLevel: agent.ThinkingLevel,
		}
	})
	spawnTool := tools.NewSpawnTool(subagentManager)
	subagents[agentID] = subagentManager
	currentAgentID := agentID
	spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
		return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
	})
	agent.Tools.Register(spawnTool)

	agent.Tools.Register(tools.NewWaitForSubagentTool(subagentManager))
	agent.Tools.Register(tools.NewListSubagentsTool(subagentManager))
	agent.Tools.Register(tools.NewCancelSubagentTool(subagentManager))

	agent.Tools.Register(tools.NewSleepTool())

	syncGroupChatTool(agent, cfg, registry, currentAgentID, groupManager)

	if agentCfg := agentConfigForID(cfg, agentID); agentCfg != nil {
		applyToolsAllowlist(agent.Tools, agentCfg.Tools, routing.NormalizeAgentID(agentCfg.ID))
	}

	subagentManager.SetTools(subagentToolClone(agent.Tools))
	subagentManager.SetSessionRecorder(agent.Sessions)
	subagentManager.SetSessionCompactor(agent.Sessions)
	subagentManager.SetTokenUsageReporter(newSubagentTokenReporter(agent.Sessions))
	subagentManager.SetCompactionConfig(
		cfg.SessionCompactionThresholdPercent(),
		cfg.CompactionModel(),
		cfg.EvictExcludedFromMemory(),
	)

	subagentManager.SetSessionEvictCallback(func(sessionKey string) {
		agent.Sessions.EvictSession(sessionKey)
	})

	subagentManager.SetSessionStatusCallback(func(sessionKey, status string) {
		agent.Sessions.SetSubagentStatus(sessionKey, status)
	})

	subagentManager.SetLedgerWiring(
		newPersistedSubagentLister(agent.Sessions),
		func(sessionKey string) bool { return agent.Sessions.SessionExists(sessionKey) },
	)
	subagentManager.SetPersistedTaskDetail(newPersistedSubagentDetail(agent.Sessions))

	subagentManager.SetPersistedTaskExecutorFallback(newPersistedSubagentExecutorPin(agent.Sessions))

	ledgerFields := map[string]interface{}{
		"agent":         agent.ID,
		"ledger_backed": subagentManager.LedgerWiringEnabled(),
		"detail_source": subagentManager.PersistedTaskDetail() != nil,
		"executor_pin":  subagentManager.PersistedTaskExecutorFallback() != nil,
	}
	if subagentManager.LedgerWiringEnabled() {
		logger.InfoCF("subagent", "subagent ledger wired: disk-backed with the task-ID collision probe", ledgerFields)
	} else {
		logger.WarnCF("subagent", "subagent ledger is memory-only: finished tasks will be lost on retention reap or restart", ledgerFields)
	}

	agent.ContextBuilder.SetToolsRegistry(agent.Tools)

	return subagentManager
}

func newPersistedSubagentLister(sessions *session.SessionManager) func(parentPrefixes []string) []tools.PersistedSubagentTask {
	if sessions == nil {
		return nil
	}
	return func(parentPrefixes []string) []tools.PersistedSubagentTask {
		metas := sessions.ListSubagentSessionMeta(parentPrefixes)
		if len(metas) == 0 {
			return nil
		}
		rows := make([]tools.PersistedSubagentTask, 0, len(metas))
		for _, meta := range metas {
			rows = append(rows, tools.PersistedSubagentTask{
				TaskID:     meta.TaskID,
				SessionKey: meta.Key,
				ParentKey:  meta.ParentKey,
				Label:      meta.Name,
				Status:     meta.Status,
				AgentID:    meta.AgentID,
				Created:    meta.Created.UnixMilli(),
				Updated:    meta.Updated.UnixMilli(),
			})
		}
		return rows
	}
}

func newPersistedSubagentExecutorPin(sessions *session.SessionManager) func(sessionKey string) string {
	if sessions == nil {
		return nil
	}
	return func(sessionKey string) string {
		return sessions.SubagentAgentPin(sessionKey)
	}
}

func newPersistedSubagentDetail(sessions *session.SessionManager) func(keys []string) map[string]tools.PersistedSubagentDetail {
	if sessions == nil {
		return nil
	}
	return func(keys []string) map[string]tools.PersistedSubagentDetail {
		details := sessions.SubagentSessionDetails(keys)
		if len(details) == 0 {
			return nil
		}
		out := make(map[string]tools.PersistedSubagentDetail, len(details))
		for key, detail := range details {
			out[key] = tools.PersistedSubagentDetail{
				Iterations: detail.Iterations,
				Summary:    detail.Summary,
			}
		}
		return out
	}
}

func syncGroupChatTool(agent *AgentInstance, cfg *config.Config, registry *AgentRegistry, agentID string, groupManager *group.GroupManager) {
	if !cfg.GroupsFeatureEnabled() || groupManager == nil {
		agent.Tools.Unregister("group_chat")
		return
	}
	if !allowlistKeeps(agentConfigForID(cfg, agentID), "group_chat") {
		agent.Tools.Unregister("group_chat")
		return
	}
	if _, exists := agent.Tools.Get("group_chat"); exists {
		return
	}
	groupChatTool := tools.NewGroupChatTool(groupManager)
	groupChatTool.SetAllowlistChecker(func(targetAgentID string) bool {
		return registry.CanSpawnSubagent(agentID, targetAgentID)
	})
	agent.Tools.Register(groupChatTool)
}

func registerSharedTools(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) (map[string]*tools.SubagentManager, map[string]*tools.BackgroundProcessManager) {
	subagents := make(map[string]*tools.SubagentManager)
	bgManagers := make(map[string]*tools.BackgroundProcessManager)
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}
		registerSharedToolsForAgent(agent, cfg, msgBus, registry, approvalManager, agentID, subagents, bgManagers, groupManager, keyringSvc)
	}
	return subagents, bgManagers
}

func subagentToolClone(reg *tools.ToolRegistry) *tools.ToolRegistry {
	return reg.CloneWithout(
		"send_file",
		"wait_for_subagent",
		"list_active_subagents",
		"cancel_subagent",
	)
}

func updateSharedToolsForAgent(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, agentID string, existingSubagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) *tools.SubagentManager {
	agent, ok := registry.GetAgent(agentID)
	if !ok {
		return nil
	}

	if _, hasSpawn := agent.Tools.Get("spawn"); hasSpawn {
		syncGroupChatTool(agent, cfg, registry, agentID, groupManager)
		if existingSm, ok := existingSubagents[agentID]; ok {
			existingSm.SetTools(subagentToolClone(agent.Tools))
			return existingSm
		}
		return nil
	}

	return registerSharedToolsForAgent(agent, cfg, msgBus, registry, approvalManager, agentID, existingSubagents, bgManagers, groupManager, keyringSvc)
}

func updateSharedTools(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, existingSubagents map[string]*tools.SubagentManager, existingBgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) (map[string]*tools.SubagentManager, map[string]*tools.BackgroundProcessManager) {
	updated := make(map[string]*tools.SubagentManager)
	bgManagers := make(map[string]*tools.BackgroundProcessManager)

	for id, sm := range existingSubagents {
		updated[id] = sm
	}

	for id, bm := range existingBgManagers {
		bgManagers[id] = bm
	}

	for _, agentID := range registry.ListAgentIDs() {
		sm := updateSharedToolsForAgent(cfg, msgBus, registry, approvalManager, agentID, existingSubagents, bgManagers, groupManager, keyringSvc)
		if sm != nil {
			updated[agentID] = sm
		}
	}

	return updated, bgManagers
}
