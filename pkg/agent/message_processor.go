package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	lelecontext "github.com/xilistudios/lele/pkg/context"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

type messageProcessor interface {
	processMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
	processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error)
	formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string
}

type messageProcessorImpl struct {
	al *AgentLoop
}

func newMessageProcessor(al *AgentLoop) *messageProcessorImpl {
	return &messageProcessorImpl{
		al: al,
	}
}

func (mp *messageProcessorImpl) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	return mp.processMessageWith(ctx, msg, true)
}

func (mp *messageProcessorImpl) processMessageWith(ctx context.Context, msg bus.InboundMessage, sendResponse bool) (string, error) {
	if msg.Channel == "system" {
		return mp.processSystemMessage(ctx, msg)
	}

	if response, handled := mp.al.commandHandler.handleCommand(ctx, msg); handled {
		return response, nil
	}

	route := mp.al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := mp.al.registry.GetAgent(route.AgentID)
	if !ok {
		agent = mp.al.registry.GetDefaultAgent()
	}

	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		sessionKey = msg.SessionKey
	}
	sessionKey = mp.al.ResolveSessionKey(sessionKey)

	mp.al.setSessionOrigin(sessionKey, tools.BuildOriginSessionKey(msg.Channel, msg.ChatID))

	if sessionAgentID, pinned := mp.al.sessionAgentOverride(sessionKey); pinned {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	agentWorkspace := ""
	if agent != nil {
		agentWorkspace = agent.Workspace
	}
	originalText := msg.Content
	mp.applyHarnessCommand(ctx, &msg, agentWorkspace)

	turnModelOverride := ""
	if msg.Metadata != nil {
		if harnessAgent := msg.Metadata["harness_agent"]; harnessAgent != "" {
			if hAgent, ok := mp.al.registry.GetAgent(harnessAgent); ok {
				agent = hAgent
			} else {
				slog.Warn("harness: unknown agent in custom command, keeping routed agent",
					"command", msg.Metadata["harness_command"], "agent", harnessAgent)
			}
		}
		if m := msg.Metadata["harness_model"]; m != "" {
			turnModelOverride = mp.al.cfg().Providers.ResolveModelAlias(m, mp.al.cfg().Agents.Defaults.Provider)
		}
	}

	var appliedCmd *appliedCommand
	if cmdName := msg.Metadata["harness_command"]; cmdName != "" {
		appliedCmd = &appliedCommand{
			Original:    originalText,
			Name:        cmdName,
			Description: msg.Metadata["harness_description"],
			Args:        msg.Metadata["harness_args"],
			Source:      msg.Metadata["harness_source"],
			Agent:       msg.Metadata["harness_agent"],
			Model:       msg.Metadata["harness_model"],
		}
	}

	mp.syncSessionModel(agent, sessionKey)

	if marker, ok := mp.al.getTurnMarker(sessionKey); ok && marker.DedupeID != "" && marker.DedupeID == msg.DedupeID {
		if !marker.ResumeNoticeSent {
			mp.al.setResumeNoticeSent(sessionKey)
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel: marker.Channel,
				ChatID:  marker.ChatID,
				Content: "⚡ El gateway se reinició a mitad de una tarea; reanudando desde el último paso completado…",
			})
		}
		mp.injectSubagentInterruptionWarning(agent, sessionKey)
		response, err := mp.ContinueTurn(ctx, marker, msg, sessionKey)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return "", err
			}
			errMsg := fmt.Sprintf("❌ Error resuming message: %v", err)
			if len(errMsg) > 4000 {
				errMsg = errMsg[:3997] + "..."
			}
			mp.persistLLMFailure(agent, sessionKey, errMsg, err, &msg)
			return errMsg, nil
		}
		mp.al.clearTurnState(sessionKey, msg.DedupeID)
		return response, nil
	}

	ephemeralNotice := mp.maybeStartEphemeralSession(agent, sessionKey)
	messageID := ""
	replyTo := ""
	if msg.Metadata != nil {
		messageID = msg.Metadata["message_id"]
		replyTo = msg.Metadata["message_id"]
	}

	mp.seedInboundTurnMarker(sessionKey, agent, msg, messageID, turnModelOverride)

	response, err := mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		SenderID:        msg.SenderID,
		UserMessage:     msg.Content,
		Attachments:     msg.Attachments,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    sendResponse,
		ReplyTo:         replyTo,
		MessageID:       messageID,
		ModelOverride:   turnModelOverride,
		AppliedCommand:  appliedCmd,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return "", err
		}
		errMsg := fmt.Sprintf("❌ Error processing message: %v", err)
		if len(errMsg) > 4000 {
			errMsg = errMsg[:3997] + "..."
		}
		mp.persistLLMFailure(agent, sessionKey, errMsg, err, &msg)
		return errMsg, nil
	}
	mp.al.clearTurnState(sessionKey, msg.DedupeID)
	mp.al.llmRunner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: sessionKey,
		Channel:    msg.Channel,
		ChatID:     msg.ChatID,
		SenderID:   msg.SenderID,
	}, lastAssistantResponse(agent, sessionKey))
	if ephemeralNotice == "" {
		return response, nil
	}
	if strings.TrimSpace(response) == "" {
		return ephemeralNotice, nil
	}
	return ephemeralNotice + "\n\n" + response, nil
}

const llmErrorMetadataKey = "llm_error"

func (mp *messageProcessorImpl) persistLLMFailure(agent *AgentInstance, sessionKey, errMsg string, err error, msg *bus.InboundMessage) {
	if msg != nil && msg.Metadata != nil {
		msg.Metadata[llmErrorMetadataKey] = "true"
	}
	if agent == nil || agent.Sessions == nil {
		return
	}

	detail := err.Error()
	if len(detail) > 4000 {
		detail = detail[:3997] + "..."
	}

	agent.Sessions.SealStreamingTail(sessionKey)
	agent.Sessions.AddFullMessage(sessionKey, providers.Message{
		Role:               "assistant",
		Content:            errMsg,
		ErrorDetail:        detail,
		ExcludeFromContext: true,
	})
	if saveErr := agent.Sessions.Save(sessionKey); saveErr != nil {
		logger.WarnCF("agent", "Failed to save session after LLM failure", map[string]interface{}{
			"session_key": sessionKey,
			"error":       saveErr.Error(),
		})
	}
}

func (mp *messageProcessorImpl) syncSessionModel(agent *AgentInstance, sessionKey string) {
	resolvedSessionKey := mp.al.ResolveSessionKey(sessionKey)
	if _, hasSessionModel := mp.al.sessionModels.Load(resolvedSessionKey); !hasSessionModel && agent != nil {
		persistedModel := ""
		if agent.Sessions != nil {
			persistedModel = agent.Sessions.GetModel(resolvedSessionKey)
		}
		if persistedModel != "" {
			persistedModel = mp.al.cfg().Providers.ResolveModelAlias(persistedModel, mp.al.cfg().Agents.Defaults.Provider)
			mp.al.sessionModels.Store(resolvedSessionKey, persistedModel)
		} else if agent.Model != "" {
			mp.al.sessionModels.Store(resolvedSessionKey, agent.Model)
		} else {
			mp.al.sessionModels.Store(resolvedSessionKey, mp.al.cfg().Agents.Defaults.Model)
		}
	}
}

func (mp *messageProcessorImpl) seedInboundTurnMarker(sessionKey string, agent *AgentInstance, msg bus.InboundMessage, messageID, modelOverride string) {
	mp.al.clearTurnMarker(sessionKey, "")
	agentID := ""
	if agent != nil {
		agentID = agent.ID
	}
	mp.al.writeTurnMarker(sessionKey, turnPhaseInbound, 0, func(m *TurnMarker) {
		m.SessionKey = sessionKey
		m.DedupeID = msg.DedupeID
		m.SpoolID = msg.SpoolID
		m.Channel = msg.Channel
		m.ChatID = msg.ChatID
		m.MsgID = messageID
		m.AgentID = agentID
		m.Model = modelOverride
	})
	mp.al.writeTurnSubagentSnapshot(sessionKey)
}

func (mp *messageProcessorImpl) injectSubagentInterruptionWarning(agent *AgentInstance, sessionKey string) {
	subs := mp.al.readTurnSubagentSnapshot(sessionKey)
	if len(subs) == 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⚠ El gateway se reinició con %d subagente(s) en curso que fueron interrumpidos y ya no existen:", len(subs))
	for _, s := range subs {
		name := s.Label
		if name == "" {
			name = s.ID
		}
		fmt.Fprintf(&b, "\n- %s: %s", name, s.Task)
	}
	b.WriteString("\nSus resultados no están disponibles. Re-lánzalos si la tarea sigue siendo necesaria; no asumas que se completaron.")

	if agent != nil && agent.Sessions != nil {
		agent.Sessions.AddMessage(sessionKey, "user", b.String())
		if err := agent.Sessions.Save(sessionKey); err != nil {
			logger.WarnCF("session", "resume: failed to persist subagent interruption warning", map[string]any{
				"session_key": sessionKey,
				"error":       err.Error(),
			})
		}
	}
	mp.al.clearTurnSubagentSnapshot(sessionKey)
}

func (mp *messageProcessorImpl) ContinueTurn(ctx context.Context, marker TurnMarker, msg bus.InboundMessage, sessionKey string) (string, error) {
	agent, ok := mp.al.registry.GetAgent(marker.AgentID)
	if !ok {
		logger.WarnCF("session", "resume: unknown agent in turn marker, using default", map[string]any{
			"session_key": sessionKey,
			"agent_id":    marker.AgentID,
		})
		agent = mp.al.registry.GetDefaultAgent()
	}

	mp.syncSessionModel(agent, sessionKey)

	return mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         marker.Channel,
		ChatID:          marker.ChatID,
		SenderID:        msg.SenderID,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    true,
		SkipUserMessage: true,
		ReplyTo:         marker.MsgID,
		MessageID:       marker.MsgID,
		ModelOverride:   marker.Model,
	})
}

func (mp *messageProcessorImpl) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	replyToMessageID := ""
	if msg.Metadata != nil {
		replyToMessageID = msg.Metadata["message_id"]
	}

	content := msg.Content
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := parts[0]
	args := parts[1:]

	agent := mp.al.registry.GetDefaultAgent()

	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = routing.BuildAgentMainSessionKey(agent.ID)
	}
	baseSessionKey := sessionKey
	sessionKey = mp.al.ResolveSessionKey(sessionKey)

	if sessionAgentID := mp.al.getSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	switch cmd {
	case "/status":
		response := mp.formatStatusResponse(agent, sessionKey, originChannel)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/subagents":
		response := formatSubagentsCommand(ctx, mp.al.toolCoordinator, sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/new":
		response := mp.handleNewCommand(agent, baseSessionKey)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/toggle":
		response := mp.handleToggleCommand(args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/clear":
		if agent != nil {
			agent.Sessions.TruncateHistory(sessionKey, 0)
			agent.Sessions.SetSummary(sessionKey, "")
			if err := agent.Sessions.Save(sessionKey); err != nil {
				logger.WarnCF("agent", "Failed to save session after clear",
					map[string]interface{}{
						"session_key": sessionKey,
						"error":       err.Error(),
					})
			}
		}
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   "✅ Conversation cleared.",
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/stop":
		subagentCount, groupCount, procCount := mp.al.toolCoordinator.cancelSessionTree(sessionKey)
		mp.al.toolCoordinator.cancelSession(sessionKey)
		response := formatStopAgentResponse(subagentCount, groupCount, procCount)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/model":
		response := mp.handleModelCommand(agent, sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/compact":
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   "🔄 Compacting conversation history...",
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})

		if agent.Sessions.GetTotalMessageCount(sessionKey) <= 4 {
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   "📭 Not enough messages to compact (need 5+).",
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		stats, err := mp.al.sessionManager.summarizeSessionWithError(agent, sessionKey)
		if err != nil {
			errorMsg := fmt.Sprintf("❌ Compaction failed: %v", err)
			if len(errorMsg) > 4000 {
				errorMsg = errorMsg[:3997] + "..."
			}
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   errorMsg,
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		if stats == nil {
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   "❌ Compaction failed or nothing to compact.",
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		response := fmt.Sprintf("📊 Memory compacted:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
			stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
			stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/verbose":
		response := mp.handleVerboseCommand(sessionKey)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/agent":
		response := mp.handleAgentCommand(baseSessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	case "/goal":
		response := mp.al.commandHandler.(*commandHandlerImpl).handleGoalCommand(ctx, originChannel, originChatID, sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	}

	return mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		SenderID:        msg.SenderID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   true,
		SendResponse:    true,
		MessageID:       msg.Metadata["message_id"],
		SkipUserMessage: false,
	})
}

func (mp *messageProcessorImpl) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	if strings.HasPrefix(content, "SYSTEM_SPAWN:") {
		return mp.handleSystemSpawn(ctx, content, sessionKey, "cli", "direct")
	}

	msg := bus.InboundMessage{
		Channel:    "cli",
		SenderID:   "cron",
		ChatID:     "direct",
		Content:    content,
		SessionKey: sessionKey,
	}

	return mp.processMessageWith(ctx, msg, false)
}

func (mp *messageProcessorImpl) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	if strings.HasPrefix(content, "SYSTEM_SPAWN:") {
		return mp.handleSystemSpawn(ctx, content, sessionKey, channel, chatID)
	}

	msg := bus.InboundMessage{
		Channel:    channel,
		SenderID:   "cron",
		ChatID:     chatID,
		Content:    content,
		SessionKey: sessionKey,
	}

	return mp.processMessage(ctx, msg)
}

func (mp *messageProcessorImpl) handleSystemSpawn(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	logger.InfoCF("agent", "Handling SYSTEM_SPAWN request",
		map[string]interface{}{
			"session_key": sessionKey,
			"channel":     channel,
			"chat_id":     chatID,
		})

	spawnConfig := mp.parseSystemSpawnMessage(content)

	agentID := mp.al.getSessionAgent(sessionKey)
	if agentID == "" {
		agentID = mp.al.getDefaultAgentID()
	}

	subagents := mp.al.toolCoordinator.GetSubagents()
	subagentManager, ok := subagents[agentID]
	if !ok || subagentManager == nil {
		subagentManager = subagents[mp.al.getDefaultAgentID()]
	}

	if subagentManager == nil {
		return "", fmt.Errorf("no subagent manager available for agent %s", agentID)
	}

	callback := func(ctx context.Context, result *tools.ToolResult) {
		var responseContent string
		if result.IsError {
			responseContent = fmt.Sprintf("❌ Scheduled task failed:\n%s", result.ForLLM)
		} else {
			responseContent = fmt.Sprintf("✅ Scheduled task completed:\n%s", result.ForLLM)
		}

		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: responseContent,
		})
	}

	spawnResult, err := subagentManager.SpawnWithOptions(
		ctx,
		spawnConfig.Task,
		spawnConfig.Label,
		spawnConfig.AgentID,
		channel,
		chatID,
		callback,
		tools.SpawnOptions{ModelOverride: spawnConfig.Model},
	)

	if err != nil {
		return "", fmt.Errorf("failed to spawn subagent: %w", err)
	}

	taskID := tools.ExtractSpawnTaskID(spawnResult)
	if taskID == "" {
		return spawnResult, nil
	}

	if task, ok := subagentManager.GetTask(taskID); ok {
		if done := task.DoneChannel(); done != nil {
			select {
			case <-done:
			case <-time.After(subagentSpawnWaitTimeout):
			case <-ctx.Done():
			}
		}
	}

	task, found := subagentManager.GetTask(taskID)
	if !found {
		return "", fmt.Errorf("scheduled task %s not found after spawn", taskID)
	}

	switch task.Status {
	case tools.SubagentStatusCompleted:
		if task.Summary != "" {
			return fmt.Sprintf("Scheduled task completed:\n%s", task.Summary), nil
		}
		return "Scheduled task completed", nil
	case tools.SubagentStatusFailed:
		msg := "scheduled task failed"
		if task.Summary != "" {
			msg += ": " + task.Summary
		}
		return "", errors.New(msg)
	case tools.SubagentStatusCancelled:
		return "", errors.New("scheduled task was cancelled")
	case tools.SubagentStatusNotDone:
		return "", errors.New("scheduled task was not completed")
	default:
		return fmt.Sprintf("Scheduled task spawned as subagent (label: %s, status: %s)", spawnConfig.Label, task.Status), nil
	}
}

const subagentSpawnWaitTimeout = 5 * time.Minute

type spawnConfig struct {
	Task     string
	Label    string
	AgentID  string
	Guidance string
	Context  string
	Model    string
}

func (mp *messageProcessorImpl) parseSystemSpawnMessage(content string) spawnConfig {
	config := spawnConfig{}

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || line == "SYSTEM_SPAWN:" {
			continue
		}

		if strings.HasPrefix(line, "TASK:") {
			config.Task = strings.TrimSpace(line[5:])
		} else if strings.HasPrefix(line, "LABEL:") {
			config.Label = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "AGENT_ID:") {
			config.AgentID = strings.TrimSpace(line[9:])
		} else if strings.HasPrefix(line, "GUIDANCE:") {
			config.Guidance = strings.TrimSpace(line[9:])
		} else if strings.HasPrefix(line, "CONTEXT:") {
			config.Context = strings.TrimSpace(line[8:])
		} else if strings.HasPrefix(line, "MODEL:") {
			config.Model = strings.TrimSpace(line[6:])
		}
	}

	if config.Label == "" {
		config.Label = utils.Truncate(config.Task, 30)
	}

	if config.Guidance != "" {
		config.Task = config.Task + "\n\nAdditional guidance: " + config.Guidance
	}

	if config.Context != "" {
		config.Task = "Context: " + config.Context + "\n\nTask: " + config.Task
	}

	return config
}

func (mp *messageProcessorImpl) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	agent := mp.al.registry.GetDefaultAgent()
	return mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      "heartbeat",
		Channel:         channel,
		ChatID:          chatID,
		UserMessage:     content,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   false,
		SendResponse:    false,
		NoHistory:       true,
	})
}

func (mp *messageProcessorImpl) formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.al.sessionManager.ModelForSession(agent, sessionKey)
	providerName := mp.al.cfg().Agents.Defaults.Provider
	if idx := strings.Index(currentModel, "/"); idx > 0 {
		providerName = currentModel[:idx]
	}
	apiKey := ""
	if provider, ok := mp.al.cfg().Providers.GetNamed(providerName); ok {
		apiKey = provider.APIKey
		if len(apiKey) > 10 {
			apiKey = apiKey[:6] + "…" + apiKey[len(apiKey)-4:]
		}
	}

	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	totalTokens := inputTokens + outputTokens

	history := agent.Sessions.GetHistoryView(sessionKey)
	historyTokens := mp.estimateTokens(history)
	summary := agent.Sessions.GetSummary(sessionKey)
	summaryTokens := 0
	if summary != "" && !hasSummaryMessage(history, summary) {
		summaryTokens = mp.estimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	systemPrompt := agent.ContextBuilder.BuildSystemPromptForSessionWithFolder(sessionKey, originChannel)
	systemTokens := mp.estimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})

	contextTokens := systemTokens + summaryTokens + historyTokens

	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	contextPercent := contextTokens * 100 / contextWindow
	if contextPercent > 100 {
		contextPercent = 100
	}
	thinkLevel := mp.al.GetProvidable().GetEffectiveThinkLevel(sessionKey)
	return fmt.Sprintf("lele %s\nGateway version: %s\n🧠 Model: %s · 🔑 api-key %s\n🧮 Tokens: ~%d in / ~%d out (~%d total)\n📚 Context: ~%d/%d (%d%%)\n🧵 Session: %s\n⚙️ Runtime: %s · Think: %s",
		gatewayVersion(), gatewayVersion(), currentModel, apiKey, inputTokens, outputTokens, totalTokens, contextTokens, contextWindow, contextPercent, sessionKey, originChannel, thinkLevel)
}

func (mp *messageProcessorImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = mp.al.cfg().Agents.Defaults.Model
	}
	mp.al.startFreshConversation(sessionKey, agent.ID, agentModel)
	return lelecontext.ContextRefreshedMessage
}

func (mp *messageProcessorImpl) handleToggleCommand(args []string) string {
	if len(args) != 1 {
		return "Usage: /toggle [ephemeral]"
	}

	switch args[0] {
	case "ephemeral":
		return mp.al.ToggleEphemeral()
	default:
		return fmt.Sprintf("Unknown toggle target: %s", args[0])
	}
}

func (mp *messageProcessorImpl) maybeStartEphemeralSession(agent *AgentInstance, sessionKey string) string {
	if agent == nil || sessionKey == "" || !mp.al.cfg().SessionEphemeralEnabled() {
		return ""
	}

	threshold := time.Duration(mp.al.cfg().SessionEphemeralThresholdSeconds()) * time.Second
	shouldReset, idle := agent.Sessions.ShouldStartFreshSession(sessionKey, threshold)
	if !shouldReset {
		return ""
	}

	if err := mp.al.resetAgentSession(agent, sessionKey); err != nil {
		logger.WarnCF("agent", "Failed to start ephemeral session", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return ""
	}

	idleSeconds := int(idle.Seconds())
	logger.InfoCF("agent", "Started fresh ephemeral session", map[string]interface{}{
		"session_key":    sessionKey,
		"idle_seconds":   idleSeconds,
		"threshold_secs": mp.al.cfg().SessionEphemeralThresholdSeconds(),
		"ephemeral_mode": true,
	})

	return fmt.Sprintf("🫧 New ephemeral session created after %d seconds of inactivity.", idleSeconds)
}

func (mp *messageProcessorImpl) handleModelCommand(agent *AgentInstance, sessionKey string, args []string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.al.sessionManager.ModelForSession(agent, sessionKey)
	if len(args) == 0 {
		return fmt.Sprintf("Current model: %s\n\nUse /model <name> to change.\nUse /models to see available options.", currentModel)
	}
	next := mp.al.cfg().Providers.ResolveModelAlias(args[0], mp.al.cfg().Agents.Defaults.Provider)
	if sessionKey == "" {
		return "Model switching requires a session context. Please start a conversation first."
	}
	mp.al.sessionModels.Store(sessionKey, next)
	return fmt.Sprintf("Model changed for this chat: %s -> %s", currentModel, next)
}

func (mp *messageProcessorImpl) handleVerboseCommand(sessionKey string) string {
	if sessionKey == "" {
		return "Verbose mode requires a session context. Please start a conversation first."
	}
	newLevel := mp.al.verboseManager.CycleLevel(sessionKey)
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

func (mp *messageProcessorImpl) handleAgentCommand(sessionKey string, args []string) string {
	if sessionKey == "" {
		return "Agent switching requires a session context. Please start a conversation first."
	}

	if len(args) == 0 {
		agentList := mp.al.registry.ListAgentIDs()
		if len(agentList) == 0 {
			return "No agents configured."
		}

		var lines []string
		lines = append(lines, "🤖 Available agents:")
		for _, id := range agentList {
			if agent, ok := mp.al.registry.GetAgent(id); ok {
				name := agent.Name
				if name == "" {
					name = id
				}
				lines = append(lines, fmt.Sprintf("- %s (%s)", id, name))
			}
		}
		lines = append(lines, "")
		lines = append(lines, "Use /agent <agent_id> to switch.")
		return strings.Join(lines, "\n")
	}

	agentID := args[0]

	agent, ok := mp.al.registry.GetAgent(agentID)
	if !ok {
		return fmt.Sprintf("❌ Agent not found: %s", agentID)
	}

	agentModel := agent.Model
	if agentModel == "" {
		agentModel = mp.al.cfg().Agents.Defaults.Model
	}
	mp.al.startFreshConversation(sessionKey, agentID, agentModel)

	agentName := agent.Name
	if agentName == "" {
		agentName = agentID
	}

	return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s", agentName, agentModel)
}

func (mp *messageProcessorImpl) estimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		if m.ExcludeFromContext {
			continue
		}
		totalChars += len(m.Content)
	}
	return totalChars * 2 / 5
}
