// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// toolCoordinator is an internal interface for tool coordination operations.
type toolCoordinator interface {
	updateToolContexts(agent *AgentInstance, channel, chatID, sessionKey string)
	stopAllSubagents() int
	cancelSession(sessionKey string)
	listRunningSubagentTasks() []*tools.SubagentTask
	getSubagentTask(taskID string) (*tools.SubagentTask, bool)
	stopSubagentTask(taskID string) bool
	continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error)
	GetStartupInfo() map[string]interface{}
	RegisterTool(tool tools.Tool)
	GetSubagents() map[string]*tools.SubagentManager
}

// toolCoordinatorImpl implements the toolCoordinator interface for handling
// tool context updates, subagent lifecycle management, and tool registration.
type toolCoordinatorImpl struct {
	al          *AgentLoop
	subagents   map[string]*tools.SubagentManager // Owned by coordinator
	registry    *AgentRegistry
	bus         *bus.MessageBus
	approvalMgr *channels.ApprovalManager
}

// newToolCoordinator creates a new tool coordinator instance.
func newToolCoordinator(al *AgentLoop) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   make(map[string]*tools.SubagentManager),
		registry:    al.registry,
		bus:         al.bus,
		approvalMgr: al.approvalManager,
	}
}

// newToolCoordinatorWithSubagents creates a tool coordinator with existing subagents.
func newToolCoordinatorWithSubagents(al *AgentLoop, subagents map[string]*tools.SubagentManager) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al:          al,
		subagents:   subagents,
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

		// Set context for all tools implementing ContextualTool interface
		if ct, ok := tool.(tools.ContextualTool); ok {
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

// cancelSession cancels any active processing for a specific session
func (tc *toolCoordinatorImpl) cancelSession(sessionKey string) {
	tc.al.cancelSession(sessionKey)
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

// registerSharedTools registers tools that are shared across all agents (web, message, spawn).
// Each agent uses its own provider for subagent spawning.
func registerSharedTools(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager) map[string]*tools.SubagentManager {
	subagents := make(map[string]*tools.SubagentManager)
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

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
		}); searchTool != nil {
			agent.Tools.Register(searchTool)
		}
		agent.Tools.Register(tools.NewWebFetchTool(50000))

		// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
		agent.Tools.Register(tools.NewI2CTool())
		agent.Tools.Register(tools.NewSPITool())

		// File tool
		sendFileTool := tools.NewSendFileTool()
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

		// Shell/Exec tool with approval support
		execTool := tools.NewExecToolWithConfig(agent.Workspace, cfg.Agents.Defaults.RestrictToWorkspace, cfg)
		if approvalManager != nil {
			execTool.SetApprovalMode(true)
		}
		agent.Tools.Register(execTool)

		// Spawn tool with allowlist checker - use agent's own provider
		subagentManager := tools.NewSubagentManager(agent.Provider, agent.Model, agent.Workspace, msgBus)
		subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
		subagentManager.SetMaxIterations(agent.MaxIterations)
		// Set callback to get context for specific agent types (each agent loads its own AGENT.md, SOUL.md, etc.)
		subagentManager.SetAgentContextCallback(func(agentID string) tools.AgentContextInfo {
			if targetAgent, ok := registry.GetAgent(agentID); ok {
				return tools.AgentContextInfo{
					Context:   targetAgent.ContextBuilder.GetInitialContext(),
					Workspace: targetAgent.Workspace,
					Name:      targetAgent.Name,
					Model:     targetAgent.Model,
					Provider:  targetAgent.Provider,
				}
			}
			// Fallback: use parent agent's context if agent not found
			return tools.AgentContextInfo{
				Context:   agent.ContextBuilder.GetInitialContext(),
				Workspace: agent.Workspace,
				Name:      agent.Name,
				Model:     agent.Model,
				Provider:  agent.Provider,
			}
		})
		spawnTool := tools.NewSpawnTool(subagentManager)
		subagents[agentID] = subagentManager
		currentAgentID := agentID
		spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
			return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
		})
		agent.Tools.Register(spawnTool)
		subagentManager.SetTools(agent.Tools.CloneWithout("send_file"))
		subagentManager.SetSessionRecorder(agent.Sessions)

		// Update context builder with the complete tools registry
		agent.ContextBuilder.SetToolsRegistry(agent.Tools)
	}
	return subagents
}
