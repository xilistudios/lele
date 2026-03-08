// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/tools"
)

// toolCoordinator is an internal interface for tool coordination operations.
type toolCoordinator interface {
	updateToolContexts(agent *AgentInstance, channel, chatID string)
	stopAllSubagents() int
	cancelSession(sessionKey string)
	listRunningSubagentTasks() []*tools.SubagentTask
	getSubagentTask(taskID string) (*tools.SubagentTask, bool)
	stopSubagentTask(taskID string) bool
	continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error)
	GetStartupInfo() map[string]interface{}
}

// toolCoordinatorImpl implements the toolCoordinator interface for handling
// tool context updates, subagent lifecycle management, and tool registration.
type toolCoordinatorImpl struct {
	al *AgentLoop
}

// newToolCoordinator creates a new tool coordinator instance.
func newToolCoordinator(al *AgentLoop) *toolCoordinatorImpl {
	return &toolCoordinatorImpl{
		al: al,
	}
}

// updateToolContexts updates the context for tools that need channel/chatID info.
func (tc *toolCoordinatorImpl) updateToolContexts(agent *AgentInstance, channel, chatID string) {
	// Use ContextualTool interface instead of type assertions
	if tool, ok := agent.Tools.Get("message"); ok {
		if mt, ok := tool.(tools.ContextualTool); ok {
			mt.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("spawn"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
	if tool, ok := agent.Tools.Get("subagent"); ok {
		if st, ok := tool.(tools.ContextualTool); ok {
			st.SetContext(channel, chatID)
		}
	}
}

// stopAllSubagents stops all running subagents and returns the count of stopped tasks.
func (tc *toolCoordinatorImpl) stopAllSubagents() int {
	totalStopped := 0
	for _, manager := range tc.al.subagents {
		if manager != nil {
			stopped := manager.StopAll()
			totalStopped += stopped
		}
	}
	return totalStopped
}

// cancelSession cancels any active processing for a specific session
func (tc *toolCoordinatorImpl) cancelSession(sessionKey string) {
	if cancel, ok := tc.al.sessionCancels.Load(sessionKey); ok {
		if cf, ok := cancel.(context.CancelFunc); ok && cf != nil {
			cf()
		}
		tc.al.sessionCancels.Delete(sessionKey)
	}
}

// listRunningSubagentTasks lists all running subagent tasks.
func (tc *toolCoordinatorImpl) listRunningSubagentTasks() []*tools.SubagentTask {
	tasks := make([]*tools.SubagentTask, 0)
	for _, manager := range tc.al.subagents {
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
	for _, manager := range tc.al.subagents {
		if task, ok := manager.GetTask(taskID); ok {
			return task, true
		}
	}
	return nil, false
}

// stopSubagentTask stops a specific subagent task.
func (tc *toolCoordinatorImpl) stopSubagentTask(taskID string) bool {
	for _, manager := range tc.al.subagents {
		if manager.StopTask(taskID) {
			return true
		}
	}
	return false
}

// continueSubagentTask continues a paused subagent with fresh guidance.
func (tc *toolCoordinatorImpl) continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error) {
	for _, manager := range tc.al.subagents {
		task, ok := manager.GetTask(taskID)
		if !ok {
			continue
		}

		callback := func(callbackCtx context.Context, result *tools.ToolResult) {
			publishSubagentAsyncResult(tc.al, sessionKey, task.OriginChannel, task.OriginChatID, result)
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
