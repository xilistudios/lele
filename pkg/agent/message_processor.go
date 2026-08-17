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
	// Public methods for direct processing (eliminates type assertions)
	ProcessDirect(ctx context.Context, content, sessionKey string) (string, error)
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error)
	formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string
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

	// Use routed session key, but honor message's session key when explicitly set
	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		sessionKey = msg.SessionKey
	}
	sessionKey = mp.al.ResolveSessionKey(sessionKey)

	// Check if a session-specific agent is set (e.g., via /agent command)
	if sessionAgentID := mp.al.getSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := mp.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	// Keep session model in sync with the active/session-selected agent unless user
	// explicitly changed model with /model.
	resolvedSessionKey := mp.al.ResolveSessionKey(sessionKey)
	if _, hasSessionModel := mp.al.sessionModels.Load(resolvedSessionKey); !hasSessionModel && agent != nil {
		// Check persisted session model before falling back to agent default.
		// This prevents the model from silently changing when continuing an
		// existing session whose in-memory entry was lost (e.g. after restart).
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

	// Delegate to llmRunner for processing
	ephemeralNotice := mp.maybeStartEphemeralSession(agent, sessionKey)
	messageID := ""
	replyTo := ""
	if msg.Metadata != nil {
		messageID = msg.Metadata["message_id"]
		replyTo = msg.Metadata["message_id"]
	}
	response, err := mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         msg.Channel,
		ChatID:          msg.ChatID,
		UserMessage:     msg.Content,
		Attachments:     msg.Attachments,
		DefaultResponse: "I've completed processing but have no response to give.",
		EnableSummary:   true,
		SendResponse:    true,
		ReplyTo:         replyTo,
		MessageID:       messageID,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			// Context cancelled (user stop, shutdown) — do not publish a
			// response; the cancellation was already acknowledged upstream.
			return "", err
		}
		// Return a user-facing error message instead of a bare error.
		// With SendResponse=true, runAgentLoop publishes the final response
		// directly to the bus. When it fails, nothing is published, which
		// leaves channel-level UI (e.g. Telegram typing indicator + placeholder)
		// stuck. Returning a non-empty string here lets Run() publish the
		// error response through the normal path, which stops the indicator.
		errMsg := fmt.Sprintf("❌ Error processing message: %v", err)
		if len(errMsg) > 4000 {
			errMsg = errMsg[:3997] + "..."
		}
		return errMsg, nil
	}
	// Caller-side goal continuation trigger. runAgentLoop has returned and
	// released the per-session semaphore, so the continuation loop can run its
	// recursive turns safely. Only triggered on the main processMessage path.
	mp.al.llmRunner.maybeRunGoalContinuation(agent, processOptions{
		SessionKey: sessionKey,
		Channel:    msg.Channel,
		ChatID:     msg.ChatID,
	}, lastAssistantResponse(agent, sessionKey))
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

	// Use default agent for system messages
	agent := mp.al.registry.GetDefaultAgent()

	// Use the session key from the message if available, otherwise use main session
	sessionKey := msg.SessionKey
	if sessionKey == "" {
		sessionKey = routing.BuildAgentMainSessionKey(agent.ID)
	}
	baseSessionKey := sessionKey
	sessionKey = mp.al.ResolveSessionKey(sessionKey)

	// Honor session-selected agent for command/system handling as well.
	if sessionAgentID := mp.al.getSessionAgent(sessionKey); sessionAgentID != "" {
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
		// Stop session-specific subagents first (delegated to toolCoordinator)
		subagentCount := mp.al.toolCoordinator.stopSessionSubagents(sessionKey)
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
			// Truncate error message if too long for Telegram
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

	// Check if subagent result was already consumed via wait_for_subagent.
	isSubagent := msg.SenderID == "subagent"
	if isSubagent {
		if msg.Metadata != nil {
			if taskID := msg.Metadata["task_id"]; taskID != "" {
				if task, ok := mp.al.toolCoordinator.getSubagentTask(taskID); ok && task != nil && task.Delivered() {
					logger.DebugCF("agent", "Skipping subagent system message - already delivered via wait_for_subagent",
						map[string]interface{}{
							"task_id":     taskID,
							"session_key": sessionKey,
						})
					return "", nil
				}
			}
		}
	}

	return mp.al.llmRunner.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         originChannel,
		ChatID:          originChatID,
		UserMessage:     fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
		DefaultResponse: "Background task completed.",
		EnableSummary:   true,
		SendResponse:    true,
		MessageID:       msg.Metadata["message_id"],
		SkipUserMessage: isSubagent,
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
	agentID := mp.al.getSessionAgent(sessionKey)
	if agentID == "" {
		agentID = mp.al.getDefaultAgentID()
	}

	subagents := mp.al.toolCoordinator.GetSubagents()
	subagentManager, ok := subagents[agentID]
	if !ok || subagentManager == nil {
		// Try default agent's subagent manager
		subagentManager = subagents[mp.al.getDefaultAgentID()]
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

	// Spawn the subagent (asynchronous; the subagent keeps running even if the
	// caller finishes first, and the callback publishes the result to the chat).
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

	// Extract the actual task ID (e.g. "subagent-3") from the human-readable
	// spawn result (e.g. "Spawned subagent task subagent-3 ('...') for task: ...").
	taskID := tools.ExtractSpawnTaskID(spawnResult)
	if taskID == "" {
		return spawnResult, nil // fallback: return the spawn message as-is
	}

	// Wait for the subagent to reach a terminal state so the cron job can
	// report its REAL outcome rather than an optimistic "spawned ok".
	// The subagent has its own execution timeout; we wait on its done channel
	// with a generous cap so a pathological task can't hold the caller forever.
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
		// still running / still waiting for context after the wait window:
		// leave the async subagent running; the callback will publish the final
		// result to the chat.
		return fmt.Sprintf("Scheduled task spawned as subagent (label: %s, status: %s)", spawnConfig.Label, task.Status), nil
	}
}

// subagentSpawnWaitTimeout caps how long a cron spawn branch will wait for its
// subagent before falling back to the async path (where the callback delivers
// the result when it eventually completes).
const subagentSpawnWaitTimeout = 5 * time.Minute

// spawnConfig holds parsed SYSTEM_SPAWN configuration
type spawnConfig struct {
	Task     string
	Label    string
	AgentID  string
	Guidance string
	Context  string
	Model    string
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
		} else if strings.HasPrefix(line, "MODEL:") {
			config.Model = strings.TrimSpace(line[6:])
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

	// Get token counts from session
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	totalTokens := inputTokens + outputTokens

	// Calculate context tokens including system prompt (initial context)
	history := agent.Sessions.GetHistoryView(sessionKey)
	historyTokens := mp.estimateTokens(history)
	summary := agent.Sessions.GetSummary(sessionKey)
	summaryTokens := 0
	if summary != "" && !hasSummaryMessage(history, summary) {
		summaryTokens = mp.estimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	// Build system prompt to get accurate token count
	systemPrompt := agent.ContextBuilder.BuildSystemPromptForSession(sessionKey, originChannel)
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
// It preserves the selected agent while switching the chat to a fresh session.
func (mp *messageProcessorImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = mp.al.cfg().Agents.Defaults.Model
	}
	mp.al.startFreshConversation(sessionKey, agent.ID, agentModel)
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

// handleModelCommand handles the /model command.
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
		agentModel = mp.al.cfg().Agents.Defaults.Model
	}
	mp.al.startFreshConversation(sessionKey, agentID, agentModel)

	agentName := agent.Name
	if agentName == "" {
		agentName = agentID
	}

	return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s", agentName, agentModel)
}

// ============================================================================
// Session utilities
// ============================================================================

// estimateTokens estimates the number of tokens in a message list.
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
