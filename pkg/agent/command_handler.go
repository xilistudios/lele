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
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/bus"
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
	if sessionAgentID := ch.al.GetSessionAgent(sessionKey); sessionAgentID != "" {
		if sessionAgent, ok := ch.al.registry.GetAgent(sessionAgentID); ok {
			agent = sessionAgent
		}
	}

	switch cmd {
	case "/new":
		return ch.handleNewCommand(agent, sessionKey), true
	case "/toggle":
		return ch.handleToggleCommand(args), true
	case "/clear":
		if agent != nil {
			agent.Sessions.TruncateHistory(sessionKey, 0)
			agent.Sessions.SetSummary(sessionKey, "")
			agent.Sessions.Save(sessionKey)
		}
		return "✅ Conversation cleared.", true
	case "/status":
		return ch.formatStatusResponse(agent, sessionKey, msg.Channel), true
	case "/model":
		return ch.handleModelCommand(agent, sessionKey, args), true
	case "/verbose":
		return ch.handleVerboseCommand(sessionKey), true
	case "/agent":
		return ch.handleAgentCommand(sessionKey, args), true
	case "/subagents":
		return formatSubagentsCommand(ctx, ch.al.toolCoordinator, sessionKey, args), true
	case "/stop":
		subagentCount := ch.al.toolCoordinator.stopAllSubagents()
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
		history := agent.Sessions.GetHistory(sessionKey)
		if len(history) <= 4 {
			return "📭 Not enough messages to compact (need 5+).", true
		}
		stats := ch.al.sessionManager.summarizeSession(agent, sessionKey)
		if stats == nil {
			return "❌ Compaction failed or nothing to compact.", true
		}
		return fmt.Sprintf("📊 Memory compacted:\n• Messages: %d → %d (dropped %d)\n• Tokens: ~%d → ~%d (saved ~%d)",
			stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
			stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens), true
	}

	return "", false
}

// handleNewCommand handles the /new command.
func (ch *commandHandlerImpl) handleNewCommand(agent *AgentInstance, sessionKey string) string {
	if agent == nil {
		return "No default agent configured"
	}
	if err := ch.al.resetAgentSession(agent, sessionKey); err != nil {
		return fmt.Sprintf("Conversation cleared, but failed to persist session state: %v", err)
	}
	return "🔄 New conversation started. Context refreshed from SOUL.md, AGENTS.md, and MEMORY.md."
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
	currentModel := ch.al.sessionManager.(*sessionManagerImpl).modelForSession(agent, sessionKey)
	if len(args) == 0 {
		var models []string
		if provider, ok := ch.al.cfg.Providers.GetNamed(ch.al.cfg.Agents.Defaults.Provider); ok {
			models = make([]string, 0, len(provider.Models))
			for alias := range provider.Models {
				models = append(models, alias)
			}
			sort.Strings(models)
		}
		if len(models) == 0 {
			return fmt.Sprintf("Current model: %s", currentModel)
		}
		return fmt.Sprintf("Current model: %s\nAvailable models: %s\nUse /model <name> to change.", currentModel, strings.Join(models, ", "))
	}
	next := ch.al.cfg.Providers.ResolveModelAlias(args[0], ch.al.cfg.Agents.Defaults.Provider)
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
		agentModel = ch.al.cfg.Agents.Defaults.Model
	}

	// Store selected agent and its model for this session
	ch.al.sessionAgents.Store(sessionKey, agentID)
	ch.al.sessionModels.Store(sessionKey, agentModel)

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
	currentModel := ch.al.sessionManager.(*sessionManagerImpl).modelForSession(agent, sessionKey)
	providerName := ch.al.cfg.Agents.Defaults.Provider
	if idx := strings.Index(currentModel, "/"); idx > 0 {
		providerName = currentModel[:idx]
	}
	apiKey := ""
	if provider, ok := ch.al.cfg.Providers.GetNamed(providerName); ok {
		apiKey = provider.APIKey
		if len(apiKey) > 10 {
			apiKey = apiKey[:6] + "…" + apiKey[len(apiKey)-4:]
		}
	}
	
	// Get token counts from session
	inputTokens, outputTokens := agent.Sessions.GetTokenCounts(sessionKey)
	totalTokens := inputTokens + outputTokens
	
	// Estimate current context from history
	history := agent.Sessions.GetHistory(sessionKey)
	contextTokens := ch.al.sessionManager.(*sessionManagerImpl).estimateTokens(history)
	
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
