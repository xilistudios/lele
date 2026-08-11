// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

// toolExecOptions aggregates all options for tool execution
type toolExecOptions struct {
	ctx          context.Context
	agent        *AgentInstance
	tc           providers.ToolCall
	channel      string
	chatID       string
	sessionKey   string
	iteration    int
	sendResponse bool
}

// toolExecutor handles the execution of tool calls and result publishing
type toolExecutor struct {
	al *AgentLoop
}

// newToolExecutor creates a new tool executor
func newToolExecutor(al *AgentLoop) *toolExecutor {
	return &toolExecutor{al: al}
}

// Execute runs a tool call, handles approval flow, and returns the result
func (te *toolExecutor) Execute(opts toolExecOptions) (*tools.ToolResult, error) {
	// Check context first
	if err := opts.ctx.Err(); err != nil {
		return nil, err
	}

	// Chat mode: only web_search and web_fetch are allowed (defense-in-depth;
	// the LLM normally never sees other tool defs in chat mode).
	if opts.agent != nil && opts.agent.Sessions != nil {
		if opts.agent.Sessions.GetMode(opts.sessionKey) == "chat" {
			if opts.tc.Name != "web_search" && opts.tc.Name != "web_fetch" {
				return tools.ErrorResult("Tool not available in chat mode"), nil
			}
		}
	}

	// Vision guard: block read_image if the session's model doesn't support vision.
	// This is defense-in-depth — the tool definition is normally filtered out in
	// llm_runner.go, but this prevents execution if the LLM somehow calls it anyway.
	if opts.tc.Name == "read_image" && opts.agent != nil && te.al != nil {
		model := te.al.sessionManager.ModelForSession(opts.agent, opts.sessionKey)
		if model != "" {
			cfg := te.al.cfg()
			providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
			if !getSupportsImages(cfg, model, providerName) {
				return tools.ErrorResult("read_image is not available: the current model does not support vision"), nil
			}
		}
	}

	// Publish tool execution notification
	te.publishExecuting(opts)

	// Create async callback for tools that implement AsyncTool
	asyncCallback := func(callbackCtx context.Context, result *tools.ToolResult) {
		if result == nil {
			return
		}
		logger.InfoCF("agent", "Async tool completed",
			map[string]interface{}{
				"tool": opts.tc.Name,
			})
		taskID := ""
		if result.Metadata != nil {
			taskID = result.Metadata["task_id"]
		}
		publishSubagentAsyncResult(te.al, opts.sessionKey, opts.channel, opts.chatID, taskID, result)
	}

	// Execute tool with approval handling for exec
	var toolResult *tools.ToolResult
	// Inject the acting agent ID and session key so tools that enforce
	// per-agent access control (e.g. the keyring secret tool) can read them.
	execCtx := tools.WithAgentToolContext(opts.ctx, opts.agent.ID, opts.sessionKey)
	if opts.tc.Name == "exec" && te.al.approvalManager != nil {
		toolResult = te.executeWithApproval(opts, asyncCallback)
	} else {
		toolResult = opts.agent.Tools.ExecuteWithContext(execCtx, opts.tc.Name, opts.tc.Arguments, opts.channel, opts.chatID, asyncCallback)
	}

	// Handle nil result
	if toolResult == nil {
		if err := opts.ctx.Err(); err != nil {
			return nil, err
		}
		toolResult = tools.ErrorResult(fmt.Sprintf("tool %s returned no result", opts.tc.Name))
	}

	// Publish result notification
	te.publishResult(opts, toolResult)

	return toolResult, nil
}

// executeWithApproval handles exec tool with approval workflow
func (te *toolExecutor) executeWithApproval(opts toolExecOptions, asyncCallback tools.AsyncCallback) *tools.ToolResult {
	toolResult := opts.agent.Tools.ExecuteWithContext(opts.ctx, opts.tc.Name, opts.tc.Arguments, opts.channel, opts.chatID, asyncCallback)

	// Check if approval is required
	if toolResult.ApprovalRequired == nil {
		return toolResult
	}

	// Send approval request to user
	approvalMsg := fmt.Sprintf("⚠️ **Se requiere aprobación**\n\n"+
		"El siguiente comando puede ser peligroso:\n"+
		"`%s`\n\n"+
		"Razón: %s",
		toolResult.ApprovalRequired.Command,
		toolResult.ApprovalRequired.Reason)

	// Parse chatID as int64 for approval manager
	var chatIDInt int64
	fmt.Sscanf(opts.chatID, "%d", &chatIDInt)

	// Create approval request
	approval := te.al.approvalManager.CreateApproval(
		opts.sessionKey,
		toolResult.ApprovalRequired.Command,
		toolResult.ApprovalRequired.Reason,
		chatIDInt,
	)

	if opts.channel == channels.ChannelName {
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.channel,
			ChatID:  opts.sessionKey,
			Event:   "approval.request",
			Metadata: map[string]string{
				"id":      approval.ID,
				"command": toolResult.ApprovalRequired.Command,
				"reason":  toolResult.ApprovalRequired.Reason,
			},
		})
	} else {
		keyboard := te.al.approvalManager.BuildApprovalKeyboard(approval.ID)
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:     opts.channel,
			ChatID:      opts.chatID,
			Content:     approvalMsg,
			ReplyMarkup: keyboard,
		})
	}

	// Wait for user response
	approved, err := approval.WaitForResponse(opts.ctx, te.al.approvalManager.GetTimeout())
	if err != nil {
		if opts.ctx.Err() != nil {
			return nil // Context cancelled
		}
		return &tools.ToolResult{
			IsError: true,
			ForLLM:  "Error: timeout esperando aprobación del usuario",
		}
	}

	if approved {
		// User approved - execute the command directly
		if execTool, ok := opts.agent.Tools.Get("exec"); ok {
			if et, ok := execTool.(*tools.ExecTool); ok {
				et.SetBypassGuard(true)
				toolResult = et.Execute(opts.ctx, opts.tc.Arguments)
				et.SetBypassGuard(false)
			}
		}
		if toolResult == nil {
			toolResult = tools.ErrorResult("Failed to execute approved command")
		}
	} else {
		// User rejected
		toolResult = &tools.ToolResult{
			IsError: true,
			ForLLM:  "El comando fue rechazado por el usuario por razones de seguridad.",
		}
	}

	return toolResult
}

// publishExecuting publishes tool execution notification to the appropriate channel
func (te *toolExecutor) publishExecuting(opts toolExecOptions) {
	argsJSON, _ := json.Marshal(opts.tc.Arguments)
	argsPreview := utils.Truncate(string(argsJSON), 200)

	logger.InfoCF("agent", fmt.Sprintf("Tool call: %s(%s)", opts.tc.Name, argsPreview),
		map[string]interface{}{
			"agent_id":  opts.agent.ID,
			"tool":      opts.tc.Name,
			"iteration": opts.iteration,
		})

	level := te.al.verboseManager.GetLevel(opts.sessionKey)
	if opts.channel == channels.ChannelName {
		actionDesc := formatBasicToolMessage(opts.tc.Name, opts.tc.Arguments)
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.channel,
			ChatID:  opts.sessionKey,
			Event:   "tool.executing",
			Metadata: map[string]string{
				"tool":         opts.tc.Name,
				"action":       actionDesc,
				"arguments":    string(argsJSON),
				"tool_call_id": opts.tc.ID,
			},
		})
	} else if level != session.VerboseOff {
		var verboseMsg string
		if level == session.VerboseFull {
			verboseMsg = fmt.Sprintf("🔧 **Tool Call (%d):** `%s`", opts.iteration, opts.tc.Name)
			if argsPreview != "" && argsPreview != "{}" {
				verboseMsg += fmt.Sprintf("\n```json\n%s\n```", argsPreview)
			}
		} else {
			verboseMsg = formatBasicToolMessage(opts.tc.Name, opts.tc.Arguments)
		}
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:        opts.channel,
			ChatID:         opts.chatID,
			Content:        verboseMsg,
			IsIntermediate: true,
		})
	}
}

// publishResult publishes tool result notification to the appropriate channel
func (te *toolExecutor) publishResult(opts toolExecOptions, toolResult *tools.ToolResult) {
	if opts.channel == channels.ChannelName {
		resultPreview := toolResult.ForLLM
		if resultPreview == "" && toolResult.Err != nil {
			resultPreview = toolResult.Err.Error()
		}
		resultPreview = utils.Truncate(resultPreview, 300)
		metadata := map[string]string{
			"tool":         opts.tc.Name,
			"result":       resultPreview,
			"tool_call_id": opts.tc.ID,
		}
		if opts.tc.Name == "spawn" && toolResult.Metadata != nil {
			if subagentSessionKey := toolResult.Metadata["subagent_session_key"]; subagentSessionKey != "" {
				metadata["subagent_session_key"] = subagentSessionKey
			}
		}
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:  opts.channel,
			ChatID:   opts.sessionKey,
			Event:    "tool.result",
			Metadata: metadata,
		})
	} else if te.al.verboseManager.IsFull(opts.sessionKey) {
		status := "✅"
		if toolResult.IsError {
			status = "❌"
		}
		resultPreview := toolResult.ForLLM
		resultPreview = utils.Truncate(resultPreview, 300)
		verboseResult := fmt.Sprintf("%s **Result:** `%s`\n```\n%s\n```", status, opts.tc.Name, resultPreview)
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:        opts.channel,
			ChatID:         opts.chatID,
			Content:        verboseResult,
			IsIntermediate: true,
		})
	}

	// Send ForUser content to user immediately if not Silent.
	// For the native channel, skip ForUser publish — the tool.result event
	// already provides visual feedback via ToolCallDisplay. ForUser is meant
	// for messaging channels (Telegram, WhatsApp) that don't have rich tool cards.
	if !toolResult.Silent && toolResult.ForUser != "" && opts.sendResponse &&
		opts.channel != channels.ChannelName && te.al.verboseManager.IsFull(opts.sessionKey) {
		te.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: opts.channel,
			ChatID:  opts.chatID,
			Content: toolResult.ForUser,
		})
		logger.DebugCF("agent", "Sent tool result to user",
			map[string]interface{}{
				"tool":        opts.tc.Name,
				"content_len": len(toolResult.ForUser),
			})
	}
}

// buildToolResultContent extracts the content to send to the LLM from a tool result
func buildToolResultContent(toolResult *tools.ToolResult) string {
	contentForLLM := toolResult.ForLLM
	if contentForLLM == "" && toolResult.Err != nil {
		contentForLLM = toolResult.Err.Error()
	}
	return contentForLLM
}
