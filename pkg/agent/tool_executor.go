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

type toolExecOptions struct {
	ctx          context.Context
	agent        *AgentInstance
	registry     *tools.ToolRegistry
	tc           providers.ToolCall
	channel      string
	chatID       string
	sessionKey   string
	senderID     string
	iteration    int
	sendResponse bool
}

func (o toolExecOptions) tools() *tools.ToolRegistry {
	if o.registry != nil {
		return o.registry
	}
	if o.agent == nil {
		return nil
	}
	return o.agent.Tools
}

type toolExecutor struct {
	al *AgentLoop
}

func newToolExecutor(al *AgentLoop) *toolExecutor {
	return &toolExecutor{al: al}
}

func (te *toolExecutor) Execute(opts toolExecOptions) (*tools.ToolResult, error) {
	if err := opts.ctx.Err(); err != nil {
		return nil, err
	}

	if opts.tc.ArgumentsTruncated {
		return tools.TruncatedArgumentsError(opts.tc), nil
	}

	if opts.agent == nil {
		return nil, fmt.Errorf("tool %s: execution requires an agent context", opts.tc.Name)
	}

	if opts.agent != nil && opts.agent.Sessions != nil {
		if opts.agent.Sessions.GetMode(opts.sessionKey) == "chat" {
			if opts.tc.Name != "web_search" && opts.tc.Name != "web_fetch" {
				return tools.ErrorResult("Tool not available in chat mode"), nil
			}
		}
	}

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

	var videoCaps *tools.VideoCapabilities
	if opts.tc.Name == "read_video" && opts.agent != nil && te.al != nil {
		model := te.al.sessionManager.ModelForSession(opts.agent, opts.sessionKey)
		if model != "" {
			cfg := te.al.cfg()
			providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
			videoSup := getSupportsVideo(cfg, model, providerName)
			visionSup := getSupportsImages(cfg, model, providerName)
			if !videoSup && !visionSup {
				return tools.ErrorResult("read_video is not available: the current model supports neither video nor vision"), nil
			}
			videoCaps = &tools.VideoCapabilities{Video: videoSup, Vision: visionSup}
		}
	}

	te.publishExecuting(opts)

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

	opts.ctx = tools.WithAgentToolContext(opts.ctx, opts.agent.ID, opts.sessionKey)
	opts.ctx = tools.WithToolSender(opts.ctx, opts.senderID)

	if videoCaps != nil {
		opts.ctx = tools.WithVideoCaps(opts.ctx, *videoCaps)
	}

	var toolResult *tools.ToolResult
	if opts.tc.Name == "exec" && te.al.approvalManager != nil {
		toolResult = te.executeWithApproval(opts, asyncCallback)
	} else {
		registry := opts.tools()
		if registry == nil {
			return nil, fmt.Errorf("tool %s: no tool registry available", opts.tc.Name)
		}
		toolResult = registry.ExecuteWithContext(opts.ctx, opts.tc.Name, opts.tc.Arguments, opts.channel, opts.chatID, asyncCallback)
	}

	if toolResult == nil {
		if err := opts.ctx.Err(); err != nil {
			return nil, err
		}
		toolResult = tools.ErrorResult(fmt.Sprintf("tool %s returned no result", opts.tc.Name))
	}

	te.publishResult(opts, toolResult)

	return toolResult, nil
}

func (te *toolExecutor) executeWithApproval(opts toolExecOptions, asyncCallback tools.AsyncCallback) *tools.ToolResult {
	registry := opts.tools()
	if registry == nil {
		return tools.ErrorResult(fmt.Sprintf("tool %s: no tool registry available", opts.tc.Name))
	}
	toolResult := registry.ExecuteWithContext(opts.ctx, opts.tc.Name, opts.tc.Arguments, opts.channel, opts.chatID, asyncCallback)

	if toolResult.ApprovalRequired == nil {
		return toolResult
	}

	approvalMsg := fmt.Sprintf("⚠️ **Se requiere aprobación**\n\n"+
		"El siguiente comando puede ser peligroso:\n"+
		"`%s`\n\n"+
		"Razón: %s",
		toolResult.ApprovalRequired.Command,
		toolResult.ApprovalRequired.Reason)

	var chatIDInt int64
	fmt.Sscanf(opts.chatID, "%d", &chatIDInt)

	approval := te.al.approvalManager.CreateApproval(
		opts.sessionKey,
		toolResult.ApprovalRequired.Command,
		toolResult.ApprovalRequired.Reason,
		chatIDInt,
		opts.senderID,
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

	approved, err := approval.WaitForResponse(opts.ctx, te.al.approvalManager.GetTimeout())
	if err != nil {
		if opts.ctx.Err() != nil {
			return nil
		}
		return &tools.ToolResult{
			IsError: true,
			ForLLM:  "Error: timeout esperando aprobación del usuario",
		}
	}

	if approved {
		approvedCmd := toolResult.ApprovalRequired.Command
		bypassCtx := tools.WithBypassGuard(opts.ctx, approvedCmd)
		if execTool, ok := registry.Get("exec"); ok {
			if et, ok := execTool.(*tools.ExecTool); ok {
				toolResult = et.Execute(bypassCtx, opts.tc.Arguments)
			}
		}
		if toolResult == nil {
			toolResult = tools.ErrorResult("Failed to execute approved command")
		}
	} else {
		toolResult = &tools.ToolResult{
			IsError: true,
			ForLLM:  "El comando fue rechazado por el usuario por razones de seguridad.",
		}
	}

	return toolResult
}

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
		te.al.publishToolLifecycle(bus.OutboundMessage{
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
		te.al.publishToolLifecycle(bus.OutboundMessage{
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

func buildToolResultContent(toolResult *tools.ToolResult) string {
	contentForLLM := toolResult.ForLLM
	if contentForLLM == "" && toolResult.Err != nil {
		contentForLLM = toolResult.Err.Error()
	}
	return contentForLLM
}
