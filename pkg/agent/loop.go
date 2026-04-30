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
	"sync"
	"sync/atomic"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/tools"
)

// AgentLoop is the main agent loop structure that orchestrates message processing.
type AgentLoop struct {
	bus               *bus.MessageBus
	cfgPtr            atomic.Pointer[config.Config]
	registry          *AgentRegistry
	state             *state.Manager
	running           atomic.Bool
	summarizing       sync.Map
	sessionAliases    sync.Map // base session key -> active session key
	sessionModels     sync.Map
	sessionAgents     sync.Map // sessionKey -> agentID for agent switching
	sessionThinking   sync.Map // sessionKey -> reasoning effort ("off", "low", "medium", "high")
	fallback          *providers.FallbackChain
	channelManager    *channels.Manager
	verboseManager    *session.VerboseManager
	sessionKeySeq     atomic.Uint64
	approvalManager   *channels.ApprovalManager // Manager for command approvals
	sessionProcessing sync.Map                  // sessionKey -> chan struct{} (semaphore per session)
	wg                sync.WaitGroup            // tracks in-flight message goroutines

	// Internal components (delegated operations)
	messageProcessor messageProcessor
	llmRunner        llmRunner
	commandHandler   commandHandler
	sessionManager   sessionManager
	toolCoordinator  toolCoordinator
	providable       *agentProvidableImpl // AgentProvidable interface implementation
}

func (al *AgentLoop) cfg() *config.Config {
	if cfg := al.cfgPtr.Load(); cfg != nil {
		return cfg
	}
	return config.DefaultConfig()
}

// ReloadRegistry updates the agent registry with new agent configurations.
// This should be called when the configuration changes (agents.list, defaults, etc.).
func (al *AgentLoop) ReloadRegistry(cfg *config.Config) {
	if cfg == nil || al.registry == nil {
		return
	}
	al.registry.ReloadAgents(cfg)
	al.cfgPtr.Store(cfg)
}

// ResolveSessionKey resolves the session key alias if one exists.
// For Native channel sessions with timestamp (native:clientId:number), skip alias
// resolution since the frontend manages these directly and they shouldn't have aliases.
func (al *AgentLoop) ResolveSessionKey(sessionKey string) string {
	if sessionKey == "" {
		return ""
	}
	if strings.HasPrefix(sessionKey, "native:") {
		parts := strings.Split(sessionKey[7:], ":")
		if len(parts) == 2 && parts[1] != "" {
			allDigits := true
			for _, c := range parts[1] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return sessionKey
			}
		}
	}
	if active, ok := al.sessionAliases.Load(sessionKey); ok {
		if resolved, ok := active.(string); ok && resolved != "" {
			return resolved
		}
	}
	return sessionKey
}

// GetSubagentParentSessionKey returns the parent session key for a subagent session.
func (al *AgentLoop) GetSubagentParentSessionKey(sessionKey string) string {
	if !strings.HasPrefix(sessionKey, "subagent:") {
		return ""
	}
	taskID := strings.TrimPrefix(sessionKey, "subagent:")
	if al.toolCoordinator == nil {
		logger.WarnCF("agent", "GetSubagentParentSessionKey: toolCoordinator is nil", map[string]interface{}{
			"session_key": sessionKey,
		})
		return ""
	}
	task, ok := al.toolCoordinator.getSubagentTask(taskID)
	if !ok || task == nil {
		logger.WarnCF("agent", "GetSubagentParentSessionKey: task not found", map[string]interface{}{
			"session_key": sessionKey,
			"task_id":     taskID,
		})
		return ""
	}
	resolved := al.ResolveSessionKey(task.OriginSessionKey)
	logger.InfoCF("agent", "GetSubagentParentSessionKey: resolved", map[string]interface{}{
		"session_key":        sessionKey,
		"task_id":            taskID,
		"origin_session_key": task.OriginSessionKey,
		"origin_channel":     task.OriginChannel,
		"origin_chat_id":     task.OriginChatID,
		"resolved_parent":    resolved,
	})
	return resolved
}

func (al *AgentLoop) nextConversationSessionKey(baseSessionKey string) string {
	if baseSessionKey == "" {
		return ""
	}
	return fmt.Sprintf("%s:chat:%d", baseSessionKey, al.sessionKeySeq.Add(1))
}

func (al *AgentLoop) startFreshConversation(baseSessionKey, agentID, model string) string {
	baseSessionKey = strings.TrimSpace(baseSessionKey)
	if baseSessionKey == "" {
		return ""
	}

	var sessionAgent *AgentInstance
	if agentID != "" {
		if a, ok := al.registry.GetAgent(agentID); ok {
			sessionAgent = a
		}
	}
	if sessionAgent == nil {
		sessionAgent = al.registry.GetDefaultAgent()
	}

	if strings.HasPrefix(baseSessionKey, "native:") {
		parts := strings.Split(baseSessionKey[7:], ":")
		if len(parts) == 2 && parts[1] != "" {
			allDigits := true
			for _, c := range parts[1] {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				if sessionAgent != nil {
					sessionAgent.Sessions.GetOrCreate(baseSessionKey)
					sessionAgent.Sessions.TruncateHistory(baseSessionKey, 0)
					sessionAgent.Sessions.SetSummary(baseSessionKey, "")
					sessionAgent.Sessions.ResetTokenCounts(baseSessionKey)
				}
				if agentID != "" {
					al.sessionAgents.Store(baseSessionKey, agentID)
				}
				if model != "" {
					al.sessionModels.Store(baseSessionKey, model)
				}
				al.sessionThinking.Delete(baseSessionKey)
				return baseSessionKey
			}
		}
	}

	newSessionKey := al.nextConversationSessionKey(baseSessionKey)
	al.sessionAliases.Store(baseSessionKey, newSessionKey)

	if agentID != "" {
		al.sessionAgents.Store(newSessionKey, agentID)
	}
	if model != "" {
		al.sessionModels.Store(newSessionKey, model)
	}
	al.sessionThinking.Delete(newSessionKey)

	if sessionAgent != nil {
		sessionAgent.Sessions.GetOrCreate(newSessionKey)
		sessionAgent.Sessions.ResetTokenCounts(newSessionKey)
		sessionAgent.Sessions.TruncateHistory(newSessionKey, 0)
		sessionAgent.Sessions.SetSummary(newSessionKey, "")
	}

	return newSessionKey
}

// processOptions configures how a message is processed
type processOptions struct {
	SessionKey      string
	Channel         string
	ChatID          string
	UserMessage     string
	Attachments     []bus.FileAttachment
	DefaultResponse string
	EnableSummary   bool
	SendResponse    bool
	NoHistory       bool
	ReplyTo         string
	MessageID       string
	SkipUserMessage bool
}

// SummarizeStats contains statistics about a summarization operation.
type SummarizeStats struct {
	BeforeMessages  int
	AfterMessages   int
	DroppedMessages int
	BeforeTokens    int
	AfterTokens     int
	SavedTokens     int
}

// NewAgentLoop creates a new agent loop instance.
func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus) *AgentLoop {
	registry := NewAgentRegistry(cfg)

	// Create approval manager early so it can be passed to tools during registration
	approvalManager := channels.NewApprovalManager()

	// Set up shared fallback chain
	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChain(cooldown)

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := registry.GetDefaultAgent()
	var stateManager *state.Manager
	var sessionManager *session.SessionManager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Workspace)
		sessionManager = defaultAgent.Sessions
	}

	// Create verbose manager with session persistence
	verboseManager := session.NewVerboseManager()
	if sessionManager != nil {
		verboseManager.SetSessionManager(sessionManager)
	}
	verboseManager.SetDefaultLevelResolver(func(sessionKey string) (session.VerboseLevel, bool) {
		if !strings.HasPrefix(sessionKey, "telegram:") {
			return session.VerboseOff, false
		}

		switch cfg.TelegramVerbose() {
		case config.VerboseBasic:
			return session.VerboseBasic, true
		case config.VerboseFull:
			return session.VerboseFull, true
		case config.VerboseOff:
			return session.VerboseOff, true
		default:
			return session.VerboseOff, false
		}
	})

	loop := &AgentLoop{
		bus:             msgBus,
		registry:        registry,
		state:           stateManager,
		summarizing:     sync.Map{},
		fallback:        fallbackChain,
		verboseManager:  verboseManager,
		approvalManager: approvalManager,
	}
	loop.cfgPtr.Store(cfg)

	// Initialize internal components
	loop.messageProcessor = newMessageProcessor(loop)
	loop.llmRunner = newLLMRunner(loop)
	loop.commandHandler = newCommandHandler(loop)
	loop.sessionManager = newSessionManager(loop)

	// Register shared tools and create tool coordinator with subagents
	subagents := registerSharedTools(cfg, msgBus, registry, approvalManager)
	loop.toolCoordinator = newToolCoordinatorWithSubagents(loop, subagents)
	loop.providable = newAgentProvidable(loop)

	return loop
}

// GetProvidable returns the AgentProvidable interface implementation.
// This is used by channel managers to access agent capabilities.
func (al *AgentLoop) GetProvidable() channels.AgentProvidable {
	return al.providable
}

// registerSessionCancel delegates to sessionManager.
func (al *AgentLoop) registerSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	return al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
}

// cancelSession delegates to sessionManager.
func (al *AgentLoop) cancelSession(sessionKey string) int {
	return al.sessionManager.CancelSession(sessionKey)
}

// Run starts the main agent loop.
func (al *AgentLoop) Run(ctx context.Context) error {
	al.running.Store(true)

	for al.running.Load() {
		select {
		case <-ctx.Done():
			return nil
		default:
			msg, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				continue
			}

			al.wg.Add(1)
			go func(m bus.InboundMessage) {
				defer al.wg.Done()

				response, err := al.messageProcessor.processMessage(ctx, m)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						logger.InfoCF("agent", "Message processing canceled",
							map[string]interface{}{
								"channel":     m.Channel,
								"chat_id":     m.ChatID,
								"session_key": m.SessionKey,
							})
						return
					}
					response = fmt.Sprintf("Error processing message: %v", err)
				}

				if response != "" {
					outboundMsg := bus.OutboundMessage{
						Channel: m.Channel,
						ChatID:  m.ChatID,
						Content: response,
					}
					if m.Metadata != nil && m.Metadata["message_id"] != "" {
						outboundMsg.MessageID = m.Metadata["message_id"]
						outboundMsg.ReplyTo = m.Metadata["message_id"]
					}
					al.bus.PublishOutbound(outboundMsg)
				}
			}(msg)
		}
	}

	return nil
}

// Stop stops the agent loop and waits for in-flight message goroutines to finish.
func (al *AgentLoop) Stop() {
	al.running.Store(false)
	al.wg.Wait()
}

// SetChannelManager sets the channel manager for the agent loop.
func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

// SetApprovalManager configures the approval manager for command approvals.
func (al *AgentLoop) SetApprovalManager(am *channels.ApprovalManager) {
	al.approvalManager = am
}

// RecordLastChannel records the last active channel for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

// RecordLastChatID records the last active chat ID for this workspace.
// This uses the atomic state save mechanism to prevent data loss on crash.
func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

// ============================================================================
// Internal Methods (for use by components within this package)
// ============================================================================

// getSessionAgent returns the agent ID for a session (internal use).
func (al *AgentLoop) getSessionAgent(sessionKey string) string {
	sessionKey = al.ResolveSessionKey(sessionKey)
	if agentID, ok := al.sessionAgents.Load(sessionKey); ok {
		result := agentID.(string)
		logger.DebugCF("agent", "getSessionAgent found in sessionAgents", map[string]interface{}{
			"session_key": sessionKey,
			"agent_id":    result,
		})
		return result
	}
	defaultID := "main"
	if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
		defaultID = defaultAgent.ID
	}
	logger.DebugCF("agent", "getSessionAgent using default", map[string]interface{}{
		"session_key": sessionKey,
		"default_id":  defaultID,
	})
	return defaultID
}

// getDefaultAgentID returns the default agent ID (internal use).
func (al *AgentLoop) getDefaultAgentID() string {
	if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
		return defaultAgent.ID
	}
	return "main"
}

// getSessionContextWindow returns the context window for a session (internal use).
func (al *AgentLoop) getSessionContextWindow(sessionKey string) int {
	resolvedSessionKey := al.ResolveSessionKey(sessionKey)
	agent := al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 128000
	}

	// Check if the session has an overridden model
	model := agent.Model
	if m, ok := al.sessionModels.Load(resolvedSessionKey); ok {
		if selected, ok := m.(string); ok && selected != "" {
			model = selected
		}
	}

	// Resolve context window from provider config, fall back to agent's value, then to default
	cfg := al.cfg()
	providerName := extractProviderFromModel(model, cfg.Agents.Defaults.Provider)
	if cw := getContextWindow(cfg, model, providerName); cw > 0 {
		return cw
	}
	if agent.ContextWindow > 0 {
		return agent.ContextWindow
	}
	return 128000
}

// RegisterTool registers a tool to all agents.
func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.toolCoordinator.RegisterTool(tool)
}

// GetSubagents returns the subagent managers (for tests).
func (al *AgentLoop) GetSubagents() map[string]*tools.SubagentManager {
	return al.toolCoordinator.GetSubagents()
}

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	return al.toolCoordinator.GetStartupInfo()
}

// agentForSession returns the agent instance for a session.
func (al *AgentLoop) agentForSession(sessionKey string) *AgentInstance {
	resolvedSessionKey := al.ResolveSessionKey(sessionKey)
	agent := al.registry.GetDefaultAgent()
	selectedAgentID := al.getSessionAgent(resolvedSessionKey)
	defaultAgentID := ""
	if agent != nil {
		defaultAgentID = agent.ID
	}
	logger.DebugCF("agent", "agentForSession debug", map[string]interface{}{
		"session_key":          sessionKey,
		"resolved_session_key": resolvedSessionKey,
		"selected_agent_id":    selectedAgentID,
		"default_agent_id":     defaultAgentID,
	})
	if selectedAgentID != "" {
		if selectedAgent, ok := al.registry.GetAgent(selectedAgentID); ok {
			agent = selectedAgent
		}
	}
	return agent
}

// resetAgentSession clears the session history and state for an agent.
func (al *AgentLoop) resetAgentSession(agent *AgentInstance, sessionKey string) error {
	previousHistory := agent.Sessions.GetHistory(sessionKey)
	previousSummary := agent.Sessions.GetSummary(sessionKey)
	agent.Sessions.TruncateHistory(sessionKey, 0)
	agent.Sessions.SetSummary(sessionKey, "")
	agent.Sessions.ResetTokenCounts(sessionKey)
	agent.ContextBuilder.ResetSystemPromptCache(sessionKey)
	// Clear any session-specific model and thinking overrides
	al.sessionModels.Delete(sessionKey)
	al.sessionThinking.Delete(sessionKey)
	if err := agent.Sessions.Save(sessionKey); err != nil {
		agent.Sessions.SetHistory(sessionKey, previousHistory)
		agent.Sessions.SetSummary(sessionKey, previousSummary)
		logger.WarnCF("agent", "Failed to save cleared session", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return err
	}
	return nil
}

// ToggleEphemeral toggles the ephemeral session mode.
func (al *AgentLoop) ToggleEphemeral() string {
	current := al.cfg().SessionEphemeralEnabled()
	next := !current
	if err := al.cfg().PersistSessionEphemeral(config.DefaultConfigPath(), next); err != nil {
		return fmt.Sprintf("Failed to update ephemeral mode in config.json: %v", err)
	}
	threshold := al.cfg().SessionEphemeralThresholdSeconds()
	if next {
		return fmt.Sprintf("🫧 Ephemeral mode enabled. Chats idle for more than %d seconds will start a fresh session on the next message.", threshold)
	}
	return "🧱 Ephemeral mode disabled. Chat history will persist across inactivity again."
}
