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
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// toolCoordinator is an internal interface for tool coordination operations.
type toolCoordinator interface {
	updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string)
	stopAllSubagents() int
	stopSessionSubagents(sessionKey string) int
	cancelAll() int
	cancelSession(sessionKey string)
	markSessionSubagentsDelivered(sessionKey string)
	listRunningSubagentTasks() []*tools.SubagentTask
	getSubagentTask(taskID string) (*tools.SubagentTask, bool)
	markSubagentDelivered(taskID string) bool
	stopSubagentTask(taskID string) bool
	continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error)
	GetStartupInfo() map[string]interface{}
	RegisterTool(tool tools.Tool)
	GetSubagents() map[string]*tools.SubagentManager
	getBackgroundExecs(includeCompleted bool) []BackgroundExecInfo
	getBackgroundExecOutput(id string, tail int) (output string, status string, elapsed time.Duration, err error)
	stopBackgroundExec(id string) error
}

// toolCoordinatorImpl implements the toolCoordinator interface for handling
// tool context updates, subagent lifecycle management, and tool registration.
type toolCoordinatorImpl struct {
	al          *AgentLoop
	subagents   map[string]*tools.SubagentManager          // Owned by coordinator
	bgManagers  map[string]*tools.BackgroundProcessManager // Keyed by agentID
	registry    *AgentRegistry
	bus         *bus.MessageBus
	approvalMgr *channels.ApprovalManager
}

// newToolCoordinator creates a new tool coordinator instance.
func newToolCoordinator(al *AgentLoop) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   make(map[string]*tools.SubagentManager),
		bgManagers:  make(map[string]*tools.BackgroundProcessManager),
		registry:    al.registry,
		bus:         al.bus,
		approvalMgr: al.approvalManager,
	}
}

// newToolCoordinatorWithSubagents creates a tool coordinator with existing subagents.
func newToolCoordinatorWithSubagents(al *AgentLoop, subagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   subagents,
		bgManagers:  bgManagers,
		registry:    al.registry,
		bus:         al.bus,
		approvalMgr: al.approvalManager,
	}
}

// updateToolContexts updates the context for all tools that implement ContextualTool.
// It iterates over all registered tools instead of hardcoding tool names.
func (tc *toolCoordinatorImpl) updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string) {
	for _, name := range agent.Tools.List() {
		tool, ok := agent.Tools.Get(name)
		if !ok {
			continue
		}

		// Set session context for tools implementing SessionAwareTool interface
		// (provides channel, chatID, AND sessionKey)
		if st, ok := tool.(tools.SessionAwareTool); ok {
			st.SetSessionContext(channel, chatID, sessionKey)
		} else if ct, ok := tool.(tools.ContextualTool); ok {
			// Fallback: set context for tools implementing only ContextualTool
			ct.SetContext(channel, chatID)
		}

		// Configure exec tool with verbose level and feedback callback
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

// stopAllSubagents stops all running subagents and returns the count of stopped tasks.
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

// stopSessionSubagents stops running subagents for a specific session.
func (tc *toolCoordinatorImpl) stopSessionSubagents(sessionKey string) int {
	resolvedKey := tc.al.ResolveSessionKey(sessionKey)
	totalStopped := 0
	for _, manager := range tc.subagents {
		if manager != nil {
			for _, task := range manager.ListTasks() {
				resolvedOrigin := tc.al.ResolveSessionKey(task.OriginSessionKey)
				taskSessionKey := task.OriginSessionKey + ":" + task.ID
				resolvedTaskKey := tc.al.ResolveSessionKey(taskSessionKey)
				if resolvedOrigin == resolvedKey || resolvedTaskKey == resolvedKey || taskSessionKey == resolvedKey {
					if manager.StopTask(task.ID) {
						totalStopped++
					}
				}
			}
		}
	}
	return totalStopped
}

// cancelAll cancels all running subagent tasks and clears the subagent map.
// Returns the count of cancelled tasks. Unlike stopAllSubagents, this also
// removes all subagent references so the map can be safely replaced.
func (tc *toolCoordinatorImpl) cancelAll() int {
	count := tc.stopAllSubagents()
	tc.subagents = make(map[string]*tools.SubagentManager)
	return count
}

// cancelSession cancels any active processing for a specific session
func (tc *toolCoordinatorImpl) cancelSession(sessionKey string) {
	tc.al.cancelSession(sessionKey)
}

// markSessionSubagentsDelivered marks all finished/terminal subagents for a specific session as delivered.
func (tc *toolCoordinatorImpl) markSessionSubagentsDelivered(sessionKey string) {
	resolvedKey := tc.al.ResolveSessionKey(sessionKey)
	for _, manager := range tc.subagents {
		if manager != nil {
			for _, task := range manager.ListTasks() {
				resolvedOrigin := tc.al.ResolveSessionKey(task.OriginSessionKey)
				taskSessionKey := task.OriginSessionKey + ":" + task.ID
				resolvedTaskKey := tc.al.ResolveSessionKey(taskSessionKey)
				if resolvedOrigin == resolvedKey || resolvedTaskKey == resolvedKey || taskSessionKey == resolvedKey {
					// Check if task status is terminal/finished (not running)
					if task.Status != tools.SubagentStatusRunning {
						manager.MarkDelivered(task.ID)
					}
				}
			}
		}
	}
}

// listRunningSubagentTasks lists all running subagent tasks.
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

// getSubagentTask gets a specific subagent task by ID.
func (tc *toolCoordinatorImpl) getSubagentTask(taskID string) (*tools.SubagentTask, bool) {
	for _, manager := range tc.subagents {
		if task, ok := manager.GetTask(taskID); ok {
			return task, true
		}
	}
	return nil, false
}

// markSubagentDelivered marks a task as delivered in the manager that owns it.
// Returns true if the task was already delivered (race lost), false if first delivery.
func (tc *toolCoordinatorImpl) markSubagentDelivered(taskID string) bool {
	for _, manager := range tc.subagents {
		if _, ok := manager.GetTask(taskID); ok {
			return manager.MarkDelivered(taskID)
		}
	}
	return false // Task not found in any manager
}

// stopSubagentTask stops a specific subagent task.
func (tc *toolCoordinatorImpl) stopSubagentTask(taskID string) bool {
	for _, manager := range tc.subagents {
		if manager.StopTask(taskID) {
			return true
		}
	}
	return false
}

// continueSubagentTask continues a paused subagent with fresh guidance.
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

// GetStartupInfo returns information about loaded tools and skills for logging.
func (tc *toolCoordinatorImpl) GetStartupInfo() map[string]interface{} {
	info := make(map[string]interface{})

	agent := tc.al.registry.GetDefaultAgent()
	if agent == nil {
		return info
	}

	// Tools info
	toolsList := agent.Tools.List()
	info["tools"] = map[string]interface{}{
		"count": len(toolsList),
		"names": toolsList,
	}

	// Skills info
	info["skills"] = agent.ContextBuilder.GetSkillsInfo()

	// Agents info
	info["agents"] = map[string]interface{}{
		"count": len(tc.al.registry.ListAgentIDs()),
		"ids":   tc.al.registry.ListAgentIDs(),
	}

	return info
}

// RegisterTool registers a tool to all agents.
func (tc *toolCoordinatorImpl) RegisterTool(tool tools.Tool) {
	for _, agentID := range tc.al.registry.ListAgentIDs() {
		if agent, ok := tc.al.registry.GetAgent(agentID); ok {
			agent.Tools.Register(tool)
		}
	}
}

// GetSubagents returns the subagent managers map.
func (tc *toolCoordinatorImpl) GetSubagents() map[string]*tools.SubagentManager {
	return tc.subagents
}

// GetBgManagers returns the background process managers map.
func (tc *toolCoordinatorImpl) GetBgManagers() map[string]*tools.BackgroundProcessManager {
	return tc.bgManagers
}

// BackgroundExecInfo contains information about a background execution.
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

// getBackgroundExecs returns all background processes across all agents.
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

// getBackgroundExecOutput returns the output of a background process by ID.
// It searches across all agents' background managers.
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

// stopBackgroundExec stops a background process by ID.
// It searches across all agents' background managers.
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

// registerSharedToolsForAgent registers all shared tools (web, hardware, file, exec, spawn, group_chat)
// for a single agent. Returns the created SubagentManager.
func registerSharedToolsForAgent(agent *AgentInstance, cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, agentID string, subagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) *tools.SubagentManager {
	// Web tools
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

	// Keyring secret tool (scoped, audited access to stored secrets)
	if keyringSvc != nil {
		agent.Tools.Register(tools.NewSecretTool(keyringSvc))
	}

	// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
	agent.Tools.Register(tools.NewI2CTool())
	agent.Tools.Register(tools.NewSPITool())

	// File tool
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

	// Shell/Exec tool with approval support and background process management
	bgManager := tools.NewBackgroundProcessManager()
	bgManagers[agentID] = bgManager

	execTool := tools.NewExecToolWithConfig(agent.Workspace, cfg.Agents.Defaults.RestrictToWorkspace, cfg)
	execTool.SetBackgroundManager(bgManager)
	if keyringSvc != nil {
		execTool.SetKeyringService(keyringSvc)
	}
	if approvalManager != nil {
		execTool.SetApprovalMode(true)
	}
	agent.Tools.Register(execTool)

	// Background exec management tools
	agent.Tools.Register(tools.NewListBackgroundExecsTool(bgManager))
	agent.Tools.Register(tools.NewGetBackgroundExecOutputTool(bgManager))
	agent.Tools.Register(tools.NewStopBackgroundExecTool(bgManager))

	// Spawn tool with allowlist checker - resolve subagent model override from SubagentsConfig.Model
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

	// Determine max iterations for subagent tasks.
	// Priority: 1) agent-specific subagent config, 2) global subagent_max_iterations, 3) agent's max_tool_iterations
	// A value of 0 means unlimited (converted to large number by NewSubagentManager).
	subagentMaxIter := 0 // default: unlimited
	if cfg.Agents.Defaults.SubagentMaxIterations > 0 {
		subagentMaxIter = cfg.Agents.Defaults.SubagentMaxIterations
	}
	if agent.Subagents != nil && agent.Subagents.MaxIterations > 0 {
		subagentMaxIter = agent.Subagents.MaxIterations
	}
	// If still 0 and agent has explicit max iterations, use that as fallback
	if subagentMaxIter == 0 && agent.MaxIterations > 0 {
		subagentMaxIter = agent.MaxIterations
	}

	subagentManager := tools.NewSubagentManager(subagentDefaultProvider, subagentDefaultModel, agent.Workspace, msgBus, subagentMaxIter)
	subagentManager.SetCompactionThresholdPercent(cfg.SessionCompactionThresholdPercent())
	subagentManager.SetRedactor(keyring.NewRedactor(keyringSvc))
	subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
	// Resolve per-task model overrides (e.g. from cron spawn jobs) into providers.
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
	// Report whether a model supports vision so subagent tool loops can
	// filter out read_image for non-vision models.
	subagentManager.SetVisionChecker(func(model string) bool {
		providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
		return getSupportsImages(cfg, model, providerName)
	})
	// Set subagent timeout from agent config (per-agent override or global default)
	if agent.Subagents != nil && agent.Subagents.TimeoutMin > 0 {
		subagentManager.SetTimeout(time.Duration(agent.Subagents.TimeoutMin) * time.Minute)
	} else if cfg.Agents.Defaults.SubagentTimeoutMinutes > 0 {
		subagentManager.SetTimeout(time.Duration(cfg.Agents.Defaults.SubagentTimeoutMinutes) * time.Minute)
	}
	// Set max concurrent subagents from agent config or global default
	if agent.Subagents != nil && agent.Subagents.MaxConcurrent > 0 {
		subagentManager.SetMaxConcurrent(agent.Subagents.MaxConcurrent)
	} else if cfg.Agents.Defaults.SubagentMaxConcurrent > 0 {
		subagentManager.SetMaxConcurrent(cfg.Agents.Defaults.SubagentMaxConcurrent)
	}
	// Set default max retries from global config
	if cfg.Agents.Defaults.SubagentMaxRetries > 0 {
		subagentManager.SetDefaultMaxRetries(cfg.Agents.Defaults.SubagentMaxRetries)
	}
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
			}
		}
		// Fallback: use subagent model override if configured, otherwise parent agent's config
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
		}
	})
	spawnTool := tools.NewSpawnTool(subagentManager)
	subagents[agentID] = subagentManager
	currentAgentID := agentID
	spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
		return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
	})
	agent.Tools.Register(spawnTool)

	// Subagent management tools: let the parent agent wait for and list
	// subagent tasks spawned via the spawn tool above.
	agent.Tools.Register(tools.NewWaitForSubagentTool(subagentManager))
	agent.Tools.Register(tools.NewListSubagentsTool(subagentManager))
	agent.Tools.Register(tools.NewCancelSubagentTool(subagentManager))

	// Sleep tool: useful for rate limiting, polling intervals, etc.
	agent.Tools.Register(tools.NewSleepTool())

	// Group collaboration tool (Mixture of Agents): lets this agent delegate a
	// problem to a multi-agent panel. Uses the shared GroupManager.
	// Registration is gated by cfg.Groups.Enabled (B10) — see syncGroupChatTool.
	syncGroupChatTool(agent, cfg, registry, currentAgentID, groupManager)

	// Subagents get all tools except send_file (user-facing) and the
	// subagent-management tools (prevent recursive wait/list overhead),
	// and background exec management tools (each subagent gets its own).
	subagentManager.SetTools(agent.Tools.CloneWithout(
		"send_file",
		"wait_for_subagent",
		"list_active_subagents",
		"cancel_subagent",
		"list_background_execs",
		"get_background_exec_output",
		"stop_background_exec",
	))
	subagentManager.SetSessionRecorder(agent.Sessions)
	// Sync subagent loop compaction to the persisted session: summary +
	// excluded-message state survive restarts, and the persisted session
	// does not grow unbounded across long-running tasks.
	subagentManager.SetSessionCompactor(agent.Sessions)
	subagentManager.SetCompactionConfig(
		cfg.SessionCompactionThresholdPercent(),
		cfg.CompactionModel(),
		cfg.EvictExcludedFromMemory(),
	)

	// Evict subagent sessions from memory when their terminal tasks are cleaned up,
	// freeing RAM. The session data remains on disk.
	subagentManager.SetSessionEvictCallback(func(sessionKey string) {
		agent.Sessions.EvictSession(sessionKey)
	})

	// Let the subagent manager detect existing session keys at spawn time, so it
	// never reuses a task ID whose session already exists (e.g. after a restart,
	// when the in-memory ID counter resets but persisted session files remain).
	subagentManager.SetSessionExistsCallback(func(sessionKey string) bool {
		return agent.Sessions.SessionExists(sessionKey)
	})

	agent.ContextBuilder.SetToolsRegistry(agent.Tools)

	return subagentManager
}

// syncGroupChatTool is the registration-time enforcement point for the groups
// feature gate (B10). group_chat is only present in an agent's toolset while
// cfg.Groups.Enabled is true; when the feature is off the tool is removed (if
// present) so no agent can invoke groups the user never activated. It is called
// both on first registration and on every reload, keeping toolsets consistent
// when the flag is toggled at runtime.
func syncGroupChatTool(agent *AgentInstance, cfg *config.Config, registry *AgentRegistry, agentID string, groupManager *group.GroupManager) {
	if !cfg.GroupsFeatureEnabled() || groupManager == nil {
		agent.Tools.Unregister("group_chat")
		return
	}
	if _, exists := agent.Tools.Get("group_chat"); exists {
		return // already registered with the same manager
	}
	groupChatTool := tools.NewGroupChatTool(groupManager)
	groupChatTool.SetAllowlistChecker(func(targetAgentID string) bool {
		return registry.CanSpawnSubagent(agentID, targetAgentID)
	})
	agent.Tools.Register(groupChatTool)
}

// registerSharedTools registers tools that are shared across all agents (web, message, spawn, group_chat).
// Each agent uses its own provider for subagent spawning.
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

// updateSharedToolsForAgent registers shared tools for a specific agent.
// It returns the SubagentManager for that agent.
// This is used during reload to update tools for new or recreated agents.
func updateSharedToolsForAgent(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, agentID string, existingSubagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) *tools.SubagentManager {
	agent, ok := registry.GetAgent(agentID)
	if !ok {
		return nil
	}

	// Check if spawn is already registered
	if _, hasSpawn := agent.Tools.Get("spawn"); hasSpawn {
		// Even when shared tools are already registered, re-sync the gated
		// group_chat tool so toggling cfg.Groups.Enabled takes effect on
		// reload without re-registering everything else (B10).
		syncGroupChatTool(agent, cfg, registry, agentID, groupManager)
		// Already has shared tools, return existing subagent manager if present
		if existingSm, ok := existingSubagents[agentID]; ok {
			return existingSm
		}
		return nil
	}

	return registerSharedToolsForAgent(agent, cfg, msgBus, registry, approvalManager, agentID, existingSubagents, bgManagers, groupManager, keyringSvc)
}

// updateSharedTools updates shared tools for all agents after a reload.
// It preserves existing subagent managers and only updates recreated agents.
func updateSharedTools(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager, existingSubagents map[string]*tools.SubagentManager, existingBgManagers map[string]*tools.BackgroundProcessManager, groupManager *group.GroupManager, keyringSvc *keyring.Service) (map[string]*tools.SubagentManager, map[string]*tools.BackgroundProcessManager) {
	updated := make(map[string]*tools.SubagentManager)
	bgManagers := make(map[string]*tools.BackgroundProcessManager)

	// Preserve existing subagent managers
	for id, sm := range existingSubagents {
		updated[id] = sm
	}

	// Preserve existing background managers
	for id, bm := range existingBgManagers {
		bgManagers[id] = bm
	}

	// Update each agent
	for _, agentID := range registry.ListAgentIDs() {
		sm := updateSharedToolsForAgent(cfg, msgBus, registry, approvalManager, agentID, existingSubagents, bgManagers, groupManager, keyringSvc)
		if sm != nil {
			updated[agentID] = sm
		}
	}

	return updated, bgManagers
}
