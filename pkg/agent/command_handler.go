// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
)

// commandHandler is the internal interface for command handling.
type commandHandler interface {
	handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool)
}

// commandHandlerImpl implements the commandHandler interface.
type commandHandlerImpl struct {
	al *AgentLoop
}

// newCommandHandler creates a new command handler.
func newCommandHandler(al *AgentLoop) *commandHandlerImpl {
	return &commandHandlerImpl{al: al}
}

// handleCommand is the main command dispatcher.
func (ch *commandHandlerImpl) handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return "", false
	}

	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", false
	}

	cmd := parts[0]
	args := parts[1:]
	route := ch.al.registry.ResolveRoute(routing.RouteInput{
		Channel:    msg.Channel,
		AccountID:  msg.Metadata["account_id"],
		Peer:       extractPeer(msg),
		ParentPeer: extractParentPeer(msg),
		GuildID:    msg.Metadata["guild_id"],
		TeamID:     msg.Metadata["team_id"],
	})

	agent, ok := ch.al.registry.GetAgent(route.AgentID)
	if !ok {
		agent = ch.al.registry.GetDefaultAgent()
	}
	sessionKey := route.SessionKey
	if msg.SessionKey != "" {
		if strings.HasPrefix(msg.SessionKey, "agent:") || strings.HasPrefix(msg.SessionKey, "telegram:") {
			sessionKey = msg.SessionKey
		}
	}
	baseSessionKey := sessionKey
	sessionKey = ch.al.ResolveSessionKey(sessionKey)
	if sessionAgentID := ch.al.getSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := ch.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	switch cmd {
	case "/new":
		return ch.handleNewCommand(agent, baseSessionKey), true
	case "/toggle":
		return ch.handleToggleCommand(args), true
	case "/clear":
		if agent != nil {
			if err := ch.al.resetAgentSession(agent, sessionKey); err != nil {
				return fmt.Sprintf("❌ Failed to clear conversation: %s", err.Error()), true
			}
		}
		return "✅ Conversation cleared.", true
	case "/status":
		return ch.formatStatusResponse(agent, sessionKey, msg.Channel), true
	case "/model":
		return ch.handleModelCommand(agent, sessionKey, args), true
	case "/verbose":
		return ch.handleVerboseCommand(sessionKey), true
	case "/think":
		return ch.handleThinkCommand(sessionKey, args), true
	case "/agent":
		return ch.handleAgentCommand(baseSessionKey, args), true
	case "/subagents":
		return formatSubagentsCommand(ctx, ch.al.toolCoordinator, sessionKey, args), true
	case "/stop":
		subagentCount := ch.al.toolCoordinator.stopSessionSubagents(sessionKey)
		ch.al.toolCoordinator.cancelSession(sessionKey)
		if subagentCount > 0 {
			return fmt.Sprintf("Agente detenido (incluye %d subagente(s)).", subagentCount), true
		}
		return "Agente detenido.", true
	case "/show":
		if len(args) < 1 {
			return "Usage: /show [model|channel|agents]", true
		}
		switch args[0] {
		case "model":
			defaultAgent := ch.al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			return fmt.Sprintf("Current model: %s", defaultAgent.Model), true
		case "channel":
			return fmt.Sprintf("Current channel: %s", msg.Channel), true
		case "agents":
			agentIDs := ch.al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown show target: %s", args[0]), true
		}
	case "/list":
		if len(args) < 1 {
			return "Usage: /list [models|channels|agents]", true
		}
		switch args[0] {
		case "models":
			return "Available models: configured in config.json per agent", true
		case "channels":
			if ch.al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			channels := ch.al.channelManager.GetEnabledChannels()
			if len(channels) == 0 {
				return "No channels enabled", true
			}
			return fmt.Sprintf("Enabled channels: %s", strings.Join(channels, ", ")), true
		case "agents":
			agentIDs := ch.al.registry.ListAgentIDs()
			return fmt.Sprintf("Registered agents: %s", strings.Join(agentIDs, ", ")), true
		default:
			return fmt.Sprintf("Unknown list target: %s", args[0]), true
		}
	case "/switch":
		if len(args) < 3 || args[1] != "to" {
			return "Usage: /switch [model|channel] to <name>", true
		}
		target := args[0]
		value := args[2]
		switch target {
		case "model":
			defaultAgent := ch.al.registry.GetDefaultAgent()
			if defaultAgent == nil {
				return "No default agent configured", true
			}
			oldModel := defaultAgent.Model
			defaultAgent.Model = value
			return fmt.Sprintf("Switched model from %s to %s", oldModel, value), true
		case "channel":
			if ch.al.channelManager == nil {
				return "Channel manager not initialized", true
			}
			if _, exists := ch.al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Sprintf("Channel '%s' not found or not enabled", value), true
			}
			return fmt.Sprintf("Switched target channel to %s", value), true
		default:
			return fmt.Sprintf("Unknown switch target: %s", target), true
		}
	case "/compact":
		if agent == nil {
			return "No agent available for compaction", true
		}
		if ch.al.sessionManager == nil {
			return "Session manager not available for compaction", true
		}
		history := agent.Sessions.GetHistoryView(sessionKey)
		if len(history) <= 4 {
			return "📭 Not enough messages to compact (need 5+).", true
		}
		// Send feedback message before starting compaction (LLM call can take seconds)
		ch.al.bus.PublishOutbound(bus.OutboundMessage{
			Channel: msg.Channel,
			ChatID:  msg.ChatID,
			Content: "🔄 Compacting conversation history...",
		})
		stats := ch.al.sessionManager.summarizeSession(agent, sessionKey)
		if stats == nil {
			return "❌ Compaction failed or nothing to compact.", true
		}
		return fmt.Sprintf("📊 Memory compacted:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
			stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
			stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens), true
	case "/group":
		// Use the resolved agent's ID (not route.AgentID which may be "main"
		// and not exist in the registry) so permission checks work correctly.
		callerID := route.AgentID
		if agent != nil {
			callerID = agent.ID
		}
		return ch.handleGroupCommand(ctx, msg, callerID, strings.Join(args, " ")), true
	}

	return "", false
}

// handleNewCommand handles the /new command.
// It preserves the selected agent while switching the chat to a fresh session.
func (ch *commandHandlerImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = ch.al.cfg().Agents.Defaults.Model
	}
	ch.al.startFreshConversation(sessionKey, agent.ID, agentModel)
	return "🔄 New conversation started. Context refreshed from AGENT.md, SOUL.md, USER.md, IDENTITY.md, and MEMORY.md."
}

func (ch *commandHandlerImpl) handleToggleCommand(args []string) string {
	if len(args) != 1 {
		return "Usage: /toggle [ephemeral]"
	}

	switch args[0] {
	case "ephemeral":
		return ch.al.ToggleEphemeral()
	default:
		return fmt.Sprintf("Unknown toggle target: %s", args[0])
	}
}

// handleModelCommand handles the /model command.
func (ch *commandHandlerImpl) handleModelCommand(agent *AgentInstance, sessionKey string, args []string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := ch.al.sessionManager.ModelForSession(agent, sessionKey)
	if len(args) == 0 {
		return fmt.Sprintf("Current model: %s\n\nUse /model <name> to change.\nUse /models to see available options.", currentModel)
	}
	next := ch.al.cfg().Providers.ResolveModelAlias(args[0], ch.al.cfg().Agents.Defaults.Provider)
	if sessionKey == "" {
		return "Model switching requires a session context. Please start a conversation first."
	}
	ch.al.sessionModels.Store(sessionKey, next)
	return fmt.Sprintf("Model changed for this chat: %s -> %s", currentModel, next)
}

// handleVerboseCommand handles the /verbose command.
func (ch *commandHandlerImpl) handleVerboseCommand(sessionKey string) string {
	if sessionKey == "" {
		return "Verbose mode requires a session context. Please start a conversation first."
	}
	newLevel := ch.al.verboseManager.CycleLevel(sessionKey)
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

// thinkLevels is the ordered cycle for /think: off → low → medium → high → off.
var thinkLevels = []string{"off", "low", "medium", "high"}

// handleThinkCommand handles the /think command, cycling or setting the reasoning effort level.
func (ch *commandHandlerImpl) handleThinkCommand(sessionKey string, args []string) string {
	if sessionKey == "" {
		return "Think mode requires a session context. Please start a conversation first."
	}

	providable := ch.al.GetProvidable()

	// If an explicit level is provided, set it directly.
	if len(args) > 0 {
		level := strings.ToLower(args[0])
		if !providable.SetThinkLevel(sessionKey, level) {
			return fmt.Sprintf("❌ Unknown think level: %s\nValid levels: off, low, medium, high", args[0])
		}
		return thinkLevelResponse(level)
	}

	// Cycle to the next level.
	current := providable.GetThinkLevel(sessionKey)
	next := "low"
	for i, l := range thinkLevels {
		if l == current {
			next = thinkLevels[(i+1)%len(thinkLevels)]
			break
		}
	}
	providable.SetThinkLevel(sessionKey, next)
	return thinkLevelResponse(next)
}

func thinkLevelResponse(level string) string {
	switch level {
	case "off":
		return "🧠 Think mode **OFF**\nUsing default reasoning level from agent configuration."
	case "low":
		return "💡 Think mode **LOW**\nMinimal reasoning effort for fast responses."
	case "medium":
		return "🤔 Think mode **MEDIUM**\nBalanced reasoning effort."
	case "high":
		return "🧩 Think mode **HIGH**\nMaximum reasoning effort for complex tasks."
	}
	return "Unknown think level"
}

// handleAgentCommand handles the /agent command.
func (ch *commandHandlerImpl) handleAgentCommand(sessionKey string, args []string) string {
	if sessionKey == "" {
		return "Agent switching requires a session context. Please start a conversation first."
	}

	// List available agents if no argument provided
	if len(args) == 0 {
		agentList := ch.al.registry.ListAgentIDs()
		if len(agentList) == 0 {
			return "No agents configured."
		}

		var lines []string
		lines = append(lines, "🤖 Available agents:")
		for _, id := range agentList {
			if agent, ok := ch.al.registry.GetAgent(id); ok {
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
	agent, ok := ch.al.registry.GetAgent(agentID)
	if !ok {
		return fmt.Sprintf("❌ Agent not found: %s", agentID)
	}

	// Get agent model
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = ch.al.cfg().Agents.Defaults.Model
	}
	ch.al.startFreshConversation(sessionKey, agentID, agentModel)

	agentName := agent.Name
	if agentName == "" {
		agentName = agentID
	}

	return fmt.Sprintf("🤖 Agent changed to: %s\n🧠 Using model: %s", agentName, agentModel)
}

// formatStatusResponse formats the status response.
func (ch *commandHandlerImpl) formatStatusResponse(agent *AgentInstance, sessionKey, originChannel string) string {
	if agent == nil {
		return "No default agent configured"
	}
	currentModel := ch.al.sessionManager.ModelForSession(agent, sessionKey)
	providerName := ch.al.cfg().Agents.Defaults.Provider
	if idx := strings.Index(currentModel, "/"); idx > 0 {
		providerName = currentModel[:idx]
	}
	apiKey := ""
	if provider, ok := ch.al.cfg().Providers.GetNamed(providerName); ok {
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
	historyTokens := ch.al.sessionManager.EstimateTokens(history)
	summary := agent.Sessions.GetSummary(sessionKey)
	summaryTokens := 0
	if summary != "" && !hasSummaryMessage(history, summary) {
		summaryTokens = ch.al.sessionManager.EstimateTokens([]providers.Message{{Role: "user", Content: summary}})
	}

	// Build system prompt to get accurate token count
	systemPrompt := agent.ContextBuilder.BuildSystemPromptForSession(sessionKey, originChannel)
	systemTokens := ch.al.sessionManager.EstimateTokens([]providers.Message{{Role: "system", Content: systemPrompt}})

	// Total context = system prompt + summary (if any) + history
	contextTokens := systemTokens + summaryTokens + historyTokens

	contextWindow := ch.al.getSessionContextWindow(sessionKey)
	contextPercent := contextTokens * 100 / contextWindow
	if contextPercent > 100 {
		contextPercent = 100
	}

	// Get think level for session
	thinkLevel := "default"
	if v, ok := ch.al.sessionThinking.Load(sessionKey); ok {
		if s, ok := v.(string); ok && s != "" {
			thinkLevel = s
		}
	} else if agent.Reasoning != nil && agent.Reasoning.Effort != nil {
		thinkLevel = *agent.Reasoning.Effort
	}

	return fmt.Sprintf("🦞 lele %s\nGateway version: %s\n🧠 Model: %s · 🔑 api-key %s\n🧮 Tokens: ~%d in / ~%d out (~%d total)\n📚 Context: ~%d/%d (%d%%)\n🧵 Session: %s\n⚙️ Runtime: %s · Think: %s",
		gatewayVersion(), gatewayVersion(), currentModel, apiKey, inputTokens, outputTokens, totalTokens, contextTokens, contextWindow, contextPercent, sessionKey, originChannel, thinkLevel)
}

// gatewayVersion returns the gateway version from build info.
func gatewayVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil || info.Main.Version == "" {
		return "dev"
	}
	if info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

// extractPeer extracts the routing peer from inbound message metadata.
func extractPeer(msg bus.InboundMessage) *routing.RoutePeer {
	peerKind := msg.Metadata["peer_kind"]
	if peerKind == "" {
		return nil
	}
	peerID := msg.Metadata["peer_id"]
	if peerID == "" {
		if peerKind == "direct" {
			peerID = msg.SenderID
		} else {
			peerID = msg.ChatID
		}
	}
	return &routing.RoutePeer{Kind: peerKind, ID: peerID}
}

// extractParentPeer extracts the parent peer (reply-to) from inbound message metadata.
func extractParentPeer(msg bus.InboundMessage) *routing.RoutePeer {
	parentKind := msg.Metadata["parent_peer_kind"]
	parentID := msg.Metadata["parent_peer_id"]
	if parentKind == "" || parentID == "" {
		return nil
	}
	return &routing.RoutePeer{Kind: parentKind, ID: parentID}
}

// handleGroupCommand dispatches /group subcommands.
// callerAgentID is the resolved agent ID of the user issuing the command (used for spawn permission checks).
func (ch *commandHandlerImpl) handleGroupCommand(ctx context.Context, msg bus.InboundMessage, callerAgentID, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "Uso: /group [list|status|stop|start] [opciones]\n" +
			"  /group list - Lista grupos activos\n" +
			"  /group status [id] - Estado de un grupo\n" +
			"  /group stop [id] - Detener un grupo\n" +
			"  /group start <profileID> <task> - Iniciar desde perfil\n" +
			"  /group start --agents a,b --strategy moa <task> - Modo ad-hoc"
	}

	parts := strings.Fields(args)
	subcmd := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = strings.Join(parts[1:], " ")
	}

	switch subcmd {
	case "help":
		return ch.handleGroupCommand(ctx, msg, callerAgentID, "")
	case "list":
		return ch.handleGroupListCommand()
	case "status":
		return ch.handleGroupStatusCommand(strings.TrimSpace(rest))
	case "stop":
		return ch.handleGroupStopCommand(strings.TrimSpace(rest))
	case "start":
		return ch.handleGroupStartCommand(ctx, msg, callerAgentID, rest)
	default:
		return fmt.Sprintf("Subcomando desconocido: %s\nUsa /group help para ver los comandos disponibles.", subcmd)
	}
}

// tagGroupSession creates a SessionManager session for the group so it shows
// up in the Group mode history (filtered by mode=group). The task is stored as
// the first user message. Best-effort: errors are logged, not fatal.
func (ch *commandHandlerImpl) tagGroupSession(groupID, task string) {
	sessionKey := "group:" + groupID
	p := ch.al.GetProvidable()
	if p == nil {
		return
	}
	if err := p.SetSessionMode(sessionKey, "group"); err != nil {
		logger.WarnCF("agent", "Failed to set group session mode", map[string]interface{}{"session_key": sessionKey, "error": err.Error()})
	}
	_ = p.AddSessionMessage(sessionKey, providers.Message{Role: "user", Content: task})
}

// handleGroupListCommand lists all active groups.
func (ch *commandHandlerImpl) handleGroupListCommand() string {
	states := ch.al.GroupManager().List()
	if len(states) == 0 {
		return "📭 No hay grupos activos."
	}
	var lines []string
	lines = append(lines, "👥 Grupos activos:")
	for _, gs := range states {
		lines = append(lines, fmt.Sprintf("  • %s | strategy=%s | status=%s | participantes=%d | turnos=%d | tokens=%d",
			gs.ID, gs.Strategy, gs.Status, len(gs.Participants), len(gs.Transcript), gs.TotalTokens))
	}
	return strings.Join(lines, "\n")
}

// handleGroupStatusCommand shows status of a specific group or lists active groups.
func (ch *commandHandlerImpl) handleGroupStatusCommand(id string) string {
	if id == "" {
		return ch.handleGroupListCommand()
	}
	gs, ok := ch.al.GroupManager().Status(id)
	if !ok {
		return fmt.Sprintf("❌ Grupo no encontrado: %s", id)
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("👥 Grupo: %s", gs.ID))
	lines = append(lines, fmt.Sprintf("  Estrategia: %s", gs.Strategy))
	lines = append(lines, fmt.Sprintf("  Estado: %s", gs.Status))
	lines = append(lines, fmt.Sprintf("  Tarea: %s", gs.Task))
	lines = append(lines, "  Participantes:")
	for _, p := range gs.Participants {
		role := p.Role
		if role == "" {
			role = "(sin rol)"
		}
		lines = append(lines, fmt.Sprintf("    - %s [%s]", p.AgentID, role))
	}
	lines = append(lines, fmt.Sprintf("  Turnos: %d", len(gs.Transcript)))
	lines = append(lines, fmt.Sprintf("  Tokens totales: %d", gs.TotalTokens))
	if t, ok := gs.LastTurn(); ok {
		lastContent := t.Content
		if len(lastContent) > 200 {
			lastContent = lastContent[:200] + "..."
		}
		lines = append(lines, fmt.Sprintf("  Último turno (%s): %s", t.Label, lastContent))
	}
	return strings.Join(lines, "\n")
}

// handleGroupStopCommand stops a group by ID.
func (ch *commandHandlerImpl) handleGroupStopCommand(id string) string {
	if id == "" {
		return "Uso: /group stop <grupo_id>"
	}
	if ch.al.GroupManager().Stop(id) {
		return fmt.Sprintf("⏹ Grupo detenido: %s", id)
	}
	return fmt.Sprintf("❌ Grupo no encontrado: %s", id)
}

// handleGroupStartCommand starts a group from a profile or ad-hoc flags.
func (ch *commandHandlerImpl) handleGroupStartCommand(ctx context.Context, msg bus.InboundMessage, callerAgentID, args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "Uso: /group start <profileID> <task>\n" +
			"  o: /group start --agents a,b --strategy moa <task>"
	}

	parts := strings.Fields(args)
	if len(parts) == 0 {
		return "Uso: /group start <profileID> <task>"
	}

	// Check if ad-hoc mode (--agents flag present)
	isAdHoc := false
	for _, p := range parts {
		if p == "--agents" {
			isAdHoc = true
			break
		}
	}

	if isAdHoc {
		return ch.handleGroupStartAdHoc(ctx, msg, callerAgentID, parts)
	}
	return ch.handleGroupStartProfile(ctx, msg, callerAgentID, parts)
}

// handleGroupStartAdHoc starts a group from ad-hoc flags.
func (ch *commandHandlerImpl) handleGroupStartAdHoc(ctx context.Context, msg bus.InboundMessage, callerAgentID string, parts []string) string {
	agentsFlag := ""
	strategy := config.StrategyRoundRobin
	rounds := 0
	moderator := ""
	parallel := false
	var taskParts []string

	i := 0
	for i < len(parts) {
		switch parts[i] {
		case "--agents":
			if i+1 < len(parts) {
				i++
				agentsFlag = parts[i]
			}
		case "--strategy":
			if i+1 < len(parts) {
				i++
				strategy = parts[i]
			}
		case "--rounds":
			if i+1 < len(parts) {
				i++
				fmt.Sscanf(parts[i], "%d", &rounds)
			}
		case "--moderator":
			if i+1 < len(parts) {
				i++
				moderator = parts[i]
			}
		case "--parallel":
			parallel = true
		default:
			taskParts = append(taskParts, parts[i])
		}
		i++
	}

	if agentsFlag == "" {
		return "❌ --agents es requerido. Ejemplo: /group start --agents agent1,agent2 --strategy moa <task>"
	}

	agentIDs := strings.Split(agentsFlag, ",")
	for i := range agentIDs {
		agentIDs[i] = strings.TrimSpace(agentIDs[i])
	}
	if len(agentIDs) == 0 {
		return "❌ Se requiere al menos un agente."
	}

	if !config.ValidStrategy(strategy) {
		return fmt.Sprintf("❌ Estrategia inválida: %s. Usa: round_robin, moa, moderator, pipeline", strategy)
	}

	task := strings.TrimSpace(strings.Join(taskParts, " "))
	if task == "" {
		return "❌ Se requiere una tarea. Ejemplo: /group start --agents a,b --strategy moa <task>"
	}

	// Permission check
	var denied []string
	for _, aid := range agentIDs {
		if !ch.al.registry.CanSpawnSubagent(callerAgentID, aid) {
			denied = append(denied, aid)
		}
	}
	if len(denied) > 0 {
		return fmt.Sprintf("❌ Sin permiso para participar con: %s", strings.Join(denied, ", "))
	}

	// Build participants
	participants := make([]group.Participant, 0, len(agentIDs))
	for _, aid := range agentIDs {
		role := ""
		if aid == moderator {
			if strategy == config.StrategyMoA {
				role = group.RoleAggregator
			} else if strategy == config.StrategyModerator {
				role = group.RoleModerator
			}
		} else {
			role = group.RoleProposer
		}
		participants = append(participants, group.Participant{
			AgentID: aid,
			Role:    role,
			Label:   aid,
		})
	}

	opts := group.GroupOptions{
		Rounds:    rounds,
		Parallel:  parallel,
		Moderator: moderator,
	}

	groupID := group.NewGroupID("adhoc")
	_, err := ch.al.GroupManager().Start(ctx, groupID, "", task, strategy, participants, opts, msg.Channel, msg.ChatID)
	if err != nil {
		return fmt.Sprintf("❌ Error al iniciar grupo: %s", err.Error())
	}
	ch.tagGroupSession(groupID, task)
	return fmt.Sprintf("✅ Grupo iniciado: %s\nEstrategia: %s · Participantes: %d\nLos turnos se mostrarán aquí en streaming. Usa /group stop %s para detenerlo.",
		groupID, strategy, len(participants), groupID)
}

// handleGroupStartProfile starts a group from a config GroupProfile.
func (ch *commandHandlerImpl) handleGroupStartProfile(ctx context.Context, msg bus.InboundMessage, callerAgentID string, parts []string) string {
	profileID := parts[0]
	task := ""
	if len(parts) > 1 {
		task = strings.TrimSpace(strings.Join(parts[1:], " "))
	}
	if task == "" {
		return fmt.Sprintf("❌ Se requiere una tarea. Uso: /group start %s <task>", profileID)
	}

	// Find the profile in config
	var profile *config.GroupProfile
	for i := range ch.al.cfg().Groups.List {
		if ch.al.cfg().Groups.List[i].ID == profileID {
			profile = &ch.al.cfg().Groups.List[i]
			break
		}
	}
	if profile == nil {
		return fmt.Sprintf("❌ Perfil de grupo no encontrado: %s\nUsa /group start --agents a,b --strategy moa <task> para ad-hoc.", profileID)
	}

	// Permission check
	var denied []string
	for _, aid := range profile.Participants {
		if !ch.al.registry.CanSpawnSubagent(callerAgentID, aid) {
			denied = append(denied, aid)
		}
	}
	if len(denied) > 0 {
		return fmt.Sprintf("❌ Sin permiso para participar con: %s", strings.Join(denied, ", "))
	}

	// Build participants with roles
	participants := make([]group.Participant, 0, len(profile.Participants))
	for _, aid := range profile.Participants {
		role := ""
		if aid == profile.Moderator {
			if profile.Strategy == config.StrategyMoA {
				role = group.RoleAggregator
			} else if profile.Strategy == config.StrategyModerator {
				role = group.RoleModerator
			}
		} else {
			role = group.RoleProposer
		}
		participants = append(participants, group.Participant{
			AgentID: aid,
			Role:    role,
			Label:   aid,
		})
	}

	opts := group.GroupOptions{
		Rounds:           profile.Rounds,
		MaxTurns:         profile.MaxTurns,
		Parallel:         profile.Parallel,
		Moderator:        profile.Moderator,
		StopKeywords:     profile.StopKeywords,
		MaxTokensPerTurn: profile.MaxTokensPerTurn,
		TotalTokenBudget: profile.TotalTokenBudget,
	}

	groupID := group.NewGroupID(profile.ID)
	_, err := ch.al.GroupManager().Start(ctx, groupID, profile.ID, task, profile.Strategy, participants, opts, msg.Channel, msg.ChatID)
	if err != nil {
		return fmt.Sprintf("❌ Error al iniciar grupo: %s", err.Error())
	}
	ch.tagGroupSession(groupID, task)
	return fmt.Sprintf("✅ Grupo iniciado: %s\nEstrategia: %s · Participantes: %d\nLos turnos se mostrarán aquí en streaming. Usa /group stop %s para detenerlo.",
		groupID, profile.Strategy, len(participants), groupID)
}
