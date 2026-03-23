// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
	"github.com/xilistudios/lele/pkg/utils"
)

// messageProcessor is the internal interface for message processing operations.
type messageProcessor interface {
	processMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
	processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error)
}

// messageProcessorImpl implements the messageProcessor interface for handling
// message routing, agent resolution, and processing orchestration.
type messageProcessorImpl struct {
	al *AgentLoop
}

// newMessageProcessor creates a new message processor instance.
func newMessageProcessor(al *AgentLoop) *messageProcessorImpl {
	return &messageProcessorImpl{
		al: al,
	}
}

// processMessage is the main entry point for processing inbound messages.
func (mp *messageProcessorImpl) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF("agent", fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]interface{}{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		})

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return mp.processSystemMessage(ctx, msg)
	}

	// Check for commands (delegated to commandHandler)
	if response, handled := mp.al.commandHandler.handleCommand(ctx, msg); handled {
		return response, nil
	}

	// Route to determine agent and session key
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

	// Use routed session key, but honor pre-set agent-scoped keys (for ProcessDirect/cron)
	// Also honor channel-specific session keys (e.g., telegram:<chat_id>)
	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		if strings.HasPrefix(msg.SessionKey, "agent:") || strings.HasPrefix(msg.SessionKey, "telegram:") {
			sessionKey = msg.SessionKey
		}
	}

	// Check if a session-specific agent is set (e.g., via /agent command)
	if sessionAgentID := mp.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	// Keep session model in sync with the active/session-selected agent unless user
	// explicitly changed model with /model.
	if _, hasSessionModel := mp.al.sessionModels.Load(sessionKey); !hasSessionModel && agent != nil {
		if agent.Model != "" {
			mp.al.sessionModels.Store(sessionKey, agent.Model)
		} else {
			mp.al.sessionModels.Store(sessionKey, mp.al.cfg.Agents.Defaults.Model)
		}
	}

	logger.InfoCF("agent", "Routed message",
		map[string]interface{}{
			"agent_id":    agent.ID,
			"session_key": sessionKey,
			"matched_by":  route.MatchedBy,
		})

	// Delegate to llmRunner for processing
	ephemeralNotice := mp.maybeStartEphemeralSession(agent, sessionKey)
	response, err := mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		Attachments:     msg.Attachments,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    false,
	})
	if err != nil {
		return "", err
	}
	if ephemeralNotice == "" {
		return response, nil
	}
	if strings.TrimSpace(response) == "" {
		return ephemeralNotice, nil
	}
	return ephemeralNotice + "\n\n" + response, nil
}

// processSystemMessage handles messages from the system channel.
func (mp *messageProcessorImpl) processSystemMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]interface{}{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Extract reply message ID from metadata if available
	replyToMessageID := ""
	if msg.Metadata != nil {
		replyToMessageID = msg.Metadata["message_id"]
	}

	// Parse command from content
	content := msg.Content
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", nil
	}
	cmd := parts[0]
	args := parts[1:]
	logger.InfoCF("agent", "System message content", map[string]interface{}{
		"content": content,
		"cmd":     cmd,
	})

	// Use default agent for system messages
	agent := mp.al.registry.GetDefaultAgent()

	// Use the session key from the message if available, otherwise use main session
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = routing.BuildAgentMainSessionKey(agent.ID)
	}

	// Honor session-selected agent for command/system handling as well.
	if sessionAgentID := mp.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	// Handle commands directly without LLM
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
		response := mp.handleNewCommand(agent, sessionKey)
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
			agent.Sessions.Save(sessionKey)
		}
		return "", nil

	case "/stop":
		// Stop all subagents first (delegated to toolCoordinator)
		subagentCount := mp.al.toolCoordinator.stopAllSubagents()
		// Cancel any active session processing
		mp.al.toolCoordinator.cancelSession(sessionKey)
		response := "Agente detenido."
		if subagentCount > 0 {
			response = fmt.Sprintf("Agente detenido (incluye %d subagente(s)).", subagentCount)
		}
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
		// Manual compaction command - use existing sessionKey from caller
		history := agent.Sessions.GetHistory(sessionKey)
		if len(history) <= 4 {
			mp.al.bus.PublishOutbound(bus.OutboundMessage{
				Channel:   originChannel,
				ChatID:    originChatID,
				Content:   "📭 Not enough messages to compact (need 5+).",
				ReplyTo:   replyToMessageID,
				MessageID: replyToMessageID,
			})
			return "", nil
		}

		stats := mp.al.sessionManager.summarizeSession(agent, sessionKey)
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
		response := mp.handleAgentCommand(sessionKey, args)
		mp.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel:   originChannel,
			ChatID:    originChatID,
			Content:   response,
			ReplyTo:   replyToMessageID,
			MessageID: replyToMessageID,
		})
		return "", nil

	}

	// For non-command messages, run through LLM
	return mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		SendResponse:    true,
	})
}

// ProcessDirect processes a message directly without going through the message bus.
func (mp *messageProcessorImpl) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	return mp.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

// ProcessDirectWithChannel processes a message directly with channel information.
func (mp *messageProcessorImpl) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	// Check for SYSTEM_SPAWN: prefix
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

// handleSystemSpawn parses SYSTEM_SPAWN: message and spawns a subagent
func (mp *messageProcessorImpl) handleSystemSpawn(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	logger.InfoCF("agent", "Handling SYSTEM_SPAWN request",
		map[string]interface{}{
			"session_key": sessionKey,
			"channel":     channel,
			"chat_id":     chatID,
		})

	// Parse the spawn configuration from the message
	spawnConfig := mp.parseSystemSpawnMessage(content)

	// Get the agent for this session to access its subagent manager
	agentID := mp.al.GetSessionAgent(sessionKey)
	if agentID == "" {
		agentID = mp.al.GetDefaultAgentID()
	}

	subagentManager, ok := mp.al.subagents[agentID]
	if !ok || subagentManager == nil {
		// Try default agent's subagent manager
		subagentManager = mp.al.subagents[mp.al.GetDefaultAgentID()]
	}

	if subagentManager == nil {
		return "", fmt.Errorf("no subagent manager available for agent %s", agentID)
	}

	// Build callback to send result to user when subagent completes
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

	// Spawn the subagent
	_, err := subagentManager.Spawn(
		ctx,
		spawnConfig.Task,
		spawnConfig.Label,
		spawnConfig.AgentID,
		channel,
		chatID,
		callback,
	)

	if err != nil {
		return "", fmt.Errorf("failed to spawn subagent: %w", err)
	}

	return fmt.Sprintf("Scheduled task spawned as subagent (label: %s)", spawnConfig.Label), nil
}

// spawnConfig holds parsed SYSTEM_SPAWN configuration
type spawnConfig struct {
	Task     string
	Label    string
	AgentID  string
	Guidance string
	Context  string
}

// parseSystemSpawnMessage parses a SYSTEM_SPAWN: message
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
		}
	}

	// If no label, generate one from task
	if config.Label == "" {
		config.Label = utils.Truncate(config.Task, 30)
	}

	// If guidance was provided, append it to the task
	if config.Guidance != "" {
		config.Task = config.Task + "\n\nAdditional guidance: " + config.Guidance
	}

	// If context was provided, prepend it
	if config.Context != "" {
		config.Task = "Context: " + config.Context + "\n\nTask: " + config.Task
	}

	return config
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
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
		NoHistory:       true, // Don't load session history for heartbeat
	})
}

// ============================================================================
// Command handlers (delegated to commandHandler but kept here for system messages)
// ============================================================================

// formatStatusResponse formats the status response for a session.
func (mp *messageProcessorImpl) formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.modelForSession(agent, sessionKey)
	providerName := mp.al.cfg.Agents.Defaults.Provider
	if idx := strings.Index(currentModel, "/"); idx > 0 {
		providerName = currentModel[:idx]
	}
	apiKey := ""
	if provider, ok := mp.al.cfg.Providers.GetNamed(providerName); ok {
		apiKey = provider.APIKey
		if len(apiKey) > 10 {
			apiKey = apiKey[:6] + "…" + apiKey[len(apiKey)-4:]
		}
	}

	// Get token counts from session
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	totalTokens := inputTokens + outputTokens

	// Calculate context tokens including system prompt (initial context)
	history := agent.Sessions.GetHistory(sessionKey)
	historyTokens := mp.estimateTokens(history)
	summary := agent.Sessions.GetSummary(sessionKey)
	summaryTokens := mp.estimateTokens([]providers.Message{{Role: "user", Content: summary}})

	// Build system prompt to get accurate token count
	systemPrompt := agent.ContextBuilder.BuildSystemPrompt()
	systemTokens := mp.estimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})

	// Total context = system prompt + summary (if any) + history
	contextTokens := systemTokens + summaryTokens + historyTokens

	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	contextPercent := contextTokens * 100 / contextWindow
	if contextPercent > 100 {
		contextPercent = 100
	}
	return fmt.Sprintf("🦞 lele %s\nGateway version: %s\n🧠 Model: %s · 🔑 api-key %s\n🧮 Tokens: ~%d in / ~%d out (~%d total)\n📚 Context: ~%d/%d (%d%%)\n🧵 Session: %s\n⚙️ Runtime: %s · Think: %s",
		gatewayVersion(), gatewayVersion(), currentModel, apiKey, inputTokens, outputTokens, totalTokens, contextTokens, contextWindow, contextPercent, sessionKey, originChannel, "medium")
}

// handleNewCommand handles the /new command.
func (mp *messageProcessorImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	if err := mp.al.resetAgentSession(agent, sessionKey); err != nil {
		return fmt.Sprintf("Conversation cleared, but failed to persist session state: %v", err)
	}
	return "🔄 New conversation started. Context refreshed from AGENT.md, SOUL.md, USER.md, IDENTITY.md, and MEMORY.md."
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
	if agent == nil || sessionKey == "" || !mp.al.cfg.SessionEphemeralEnabled() {
		return ""
	}

	threshold := time.Duration(mp.al.cfg.SessionEphemeralThresholdSeconds()) * time.Second
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
		"threshold_secs": mp.al.cfg.SessionEphemeralThresholdSeconds(),
		"ephemeral_mode": true,
	})

	return fmt.Sprintf("🫧 New ephemeral session created after %d seconds of inactivity.", idleSeconds)
}

// handleModelCommand handles the /model command.
func (mp *messageProcessorImpl) handleModelCommand(agent *AgentInstance, sessionKey string, args []string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := mp.modelForSession(agent, sessionKey)
	if len(args) == 0 {
		var models []string
		if provider, ok := mp.al.cfg.Providers.GetNamed(mp.al.cfg.Agents.Defaults.Provider); ok {
			models = make([]string, 0, len(provider.Models))
			for alias := range provider.Models {
				models = append(models, alias)
			}
			// Sort for consistent output
			for i := 0; i < len(models)-1; i++ {
				for j := i + 1; j < len(models); j++ {
					if models[i] > models[j] {
						models[i], models[j] = models[j], models[i]
					}
				}
			}
		}
		if len(models) == 0 {
			return fmt.Sprintf("Current model: %s", currentModel)
		}
		return fmt.Sprintf("Current model: %s\nAvailable models: %s\nUse /model <name> to change.", currentModel, strings.Join(models, ", "))
	}
	next := mp.al.cfg.Providers.ResolveModelAlias(args[0], mp.al.cfg.Agents.Defaults.Provider)
	if sessionKey == "" {
		return "Model switching requires a session context. Please start a conversation first."
	}
	mp.al.sessionModels.Store(sessionKey, next)
	return fmt.Sprintf("Model changed for this chat: %s -> %s", currentModel, next)
}

// handleVerboseCommand handles the /verbose command.
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

// handleAgentCommand handles the /agent command.
func (mp *messageProcessorImpl) handleAgentCommand(sessionKey string, args []string) string {
	if sessionKey == "" {
		return "Agent switching requires a session context. Please start a conversation first."
	}

	// List available agents if no argument provided
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

	// Validate agent exists
	agent, ok := mp.al.registry.GetAgent(agentID)
	if !ok {
		return fmt.Sprintf("❌ Agent not found: %s", agentID)
	}

	// Get agent model
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = mp.al.cfg.Agents.Defaults.Model
	}

	// Reset the new agent's session so old history for this session key doesn't bleed through.
	// This also clears any stale model override; we set the new model explicitly below.
	if err := mp.al.resetAgentSession(agent, sessionKey); err != nil {
		agentName := agent.Name
		if agentName == "" {
			agentName = agentID
		}
		// Bind agent even if session reset failed
		mp.al.sessionAgents.Store(sessionKey, agentID)
		mp.al.sessionModels.Store(sessionKey, agentModel)
		return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s\n⚠️ Warning: failed to clear session: %v", agentName, agentModel, err)
	}

	// Bind after reset so sessionModels is not wiped by resetAgentSession
	mp.al.sessionAgents.Store(sessionKey, agentID)
	mp.al.sessionModels.Store(sessionKey, agentModel)

	agentName := agent.Name
	if agentName == "" {
		agentName = agentID
	}

	return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s", agentName, agentModel)
}

// ============================================================================
// Session utilities
// ============================================================================

// modelForSession returns the model to use for a session.
func (mp *messageProcessorImpl) modelForSession(agent *AgentInstance, sessionKey string) string {
	if sessionKey != "" {
		if model, ok := mp.al.sessionModels.Load(sessionKey); ok {
			if selected, ok := model.(string); ok && selected != "" {
				return selected
			}
		}
	}
	return agent.Model
}

// estimateTokens estimates the number of tokens in a message list.
func (mp *messageProcessorImpl) estimateTokens(messages []providers.Message) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Content)
	}
	// 2.5 chars per token = totalChars * 2 / 5
	return totalChars * 2 / 5
}
