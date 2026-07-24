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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	bus                  *bus.MessageBus
	cfgPtr               atomic.Pointer[config.Config]
	registry             *AgentRegistry
	state                *state.Manager
	running              atomic.Bool
	summarizing          sync.Map
	sessionAliases       sync.Map // base session key -> active session key
	sessionModels        sync.Map
	sessionAgents        sync.Map // sessionKey -> agentID for agent switching
	sessionThinking      sync.Map // sessionKey -> reasoning effort ("off", "low", "medium", "high")
	fallback             *providers.FallbackChain
	channelManager       *channels.Manager
	verboseManager       *session.VerboseManager
	sessionKeySeq        atomic.Uint64
	approvalManager      *channels.ApprovalManager // Manager for command approvals
	sessionProcessing    sync.Map                  // sessionKey -> chan struct{} (semaphore per session)
	subagentSessionAgent sync.Map                  // subagent session key -> agent ID (O(1) lookup, not O(N))
	wg                   sync.WaitGroup            // tracks in-flight message goroutines

	// Internal components (delegated operations)
	messageProcessor   messageProcessor
	llmRunner          llmRunner
	commandHandler     commandHandler
	sessionManager     sessionManager
	toolCoordinator    toolCoordinator
	providable         *agentProvidableImpl // AgentProvidable interface implementation
	stopSessionCleanup func()               // stops the background session cleanup goroutine
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

	// Cancel all running subagents before reloading the tool coordinator.
	// This prevents goroutine leaks from the old coordinator's subagent managers.
	if al.toolCoordinator != nil {
		al.toolCoordinator.cancelAll()
	}

	al.registry.ReloadAgents(cfg)
	al.cfgPtr.Store(cfg)

	// Re-register shared tools for new/recreated agents
	existingSubagents := al.toolCoordinator.GetSubagents()
	existingBgManagers := al.toolCoordinator.(*toolCoordinatorImpl).bgManagers
	updatedSubagents, updatedBgManagers := updateSharedTools(cfg, al.bus, al.registry, al.approvalManager, existingSubagents, existingBgManagers)

	// Wire up session key and cancel callbacks for all subagents
	for agentID, sm := range updatedSubagents {
		id := agentID // capture loop variable
		sm.SetSessionKeyCallback(func(sessionKey, _ string) {
			al.subagentSessionAgent.Store(sessionKey, id)
		})
		sm.SetRegisterSessionCancelCallback(func(sessionKey string, cancel context.CancelFunc) func() {
			return al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
		})
	}

	// Update tool coordinator with new subagents
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, updatedSubagents, updatedBgManagers)
}

// ResolveSessionKey resolves the session key alias if one exists.
func (al *AgentLoop) ResolveSessionKey(sessionKey string) string {
	if sessionKey == "" {
		return ""
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
	var taskID string
	if strings.HasPrefix(sessionKey, "subagent:") {
		taskID = strings.TrimPrefix(sessionKey, "subagent:")
	} else if idx := strings.LastIndex(sessionKey, ":subagent-"); idx > 0 {
		taskID = sessionKey[idx+1:]
	}
	if taskID == "" {
		return ""
	}

	if al.toolCoordinator != nil {
		task, ok := al.toolCoordinator.getSubagentTask(taskID)
		if ok && task != nil {
			resolved := al.ResolveSessionKey(task.OriginSessionKey)
			logger.InfoCF("agent", "GetSubagentParentSessionKey: resolved from task", map[string]interface{}{
				"session_key":        sessionKey,
				"task_id":            taskID,
				"origin_session_key": task.OriginSessionKey,
				"origin_channel":     task.OriginChannel,
				"origin_chat_id":     task.OriginChatID,
				"resolved_parent":    resolved,
			})
			return resolved
		}
	}

	// Fallback: parse parent from session key structure {parent}:{taskID}
	// Only use this fallback if taskID matches the expected subagent format
	if matched, _ := regexp.MatchString(`^subagent-\d+$`, taskID); matched {
		if idx := strings.LastIndex(sessionKey, ":"+taskID); idx > 0 {
			parent := sessionKey[:idx]
			logger.InfoCF("agent", "GetSubagentParentSessionKey: resolved from session key", map[string]interface{}{
				"session_key": sessionKey,
				"task_id":     taskID,
				"parent_key":  parent,
			})
			return parent
		}
	}

	logger.WarnCF("agent", "GetSubagentParentSessionKey: unable to resolve parent", map[string]interface{}{
		"session_key": sessionKey,
		"task_id":     taskID,
	})
	return ""
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

	// Backward compatibility: handle old native:<uuid>:<digits> format
	// Old sessions on disk with this format should still be cleaned up correctly.
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
				// Old format - reset the session in-place, no new key generation
				var sessionAgent *AgentInstance
				if agentID != "" {
					if a, ok := al.registry.GetAgent(agentID); ok {
						sessionAgent = a
					}
				}
				if sessionAgent == nil {
					sessionAgent = al.registry.GetDefaultAgent()
				}

				// Reset session state on the existing key
				if sessionAgent != nil {
					sessionAgent.Sessions.GetOrCreate(baseSessionKey)
					sessionAgent.Sessions.ResetTokenCounts(baseSessionKey)
					sessionAgent.Sessions.TruncateHistory(baseSessionKey, 0)
					sessionAgent.Sessions.SetSummary(baseSessionKey, "")
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

	var sessionAgent *AgentInstance
	if agentID != "" {
		if a, ok := al.registry.GetAgent(agentID); ok {
			sessionAgent = a
		}
	}
	if sessionAgent == nil {
		sessionAgent = al.registry.GetDefaultAgent()
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
		// GetOrCreate ensures the session exists in memory.
		// Do NOT save to disk yet — the session should only persist
		// once the user actually sends a message.
		sessionAgent.Sessions.GetOrCreate(newSessionKey)
		sessionAgent.Sessions.ResetTokenCounts(newSessionKey)
		// Truncate history and clear summary so the new session is truly
		// fresh.  This is necessary because loadSessions() may have already
		// populated the session from a previous run's on-disk files.
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

	// Create a single, shared session manager for all agents.
	// Sessions are stored in <lele-dir>/sessions (unified, not per-workspace).
	// Migration from old per-workspace session dirs happens here too.
	unifiedSessionsDir := filepath.Join(config.GetLeleDir(), "sessions")
	sharedSessionManager := session.NewSessionManager(unifiedSessionsDir)

	// Migrate sessions from old per-workspace locations to the unified directory.
	// Run in background — migration is best-effort and should not block startup.
	go func() {
		for _, agentID := range registry.ListAgentIDs() {
			if agent, ok := registry.GetAgent(agentID); ok && agent != nil {
				oldSessionsDir := filepath.Join(agent.Workspace, "sessions")
				session.MigrateFromWorkspace(oldSessionsDir, unifiedSessionsDir)
			}
		}
	}()

	// Replace per-agent session managers with the shared one.
	registry.SetSharedSessionManager(sharedSessionManager)

	// Start background goroutine to periodically evict idle sessions
	// and clean up orphaned metadata. Runs every 5 minutes.
	stopCleanup := sharedSessionManager.StartCleanupGoroutine(5 * time.Minute)

	// Create state manager using default agent's workspace for channel recording
	defaultAgent := registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Workspace)
	}

	// Create verbose manager with session persistence
	sessionManager := sharedSessionManager
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
	subagents, bgManagers := registerSharedTools(cfg, msgBus, registry, approvalManager)

	// Wire up session key and cancel callbacks so the agent layer can build an O(1)
	// subagent session-to-agent mapping for GetSessionHistory.
	for agentID, sm := range subagents {
		id := agentID // capture loop variable
		sm.SetSessionKeyCallback(func(sessionKey, _ string) {
			loop.subagentSessionAgent.Store(sessionKey, id)
		})
		sm.SetRegisterSessionCancelCallback(func(sessionKey string, cancel context.CancelFunc) func() {
			return loop.sessionManager.RegisterSessionCancel(sessionKey, cancel)
		})
	}

	loop.toolCoordinator = newToolCoordinatorWithSubagents(loop, subagents, bgManagers)
	loop.providable = newAgentProvidable(loop)
	loop.stopSessionCleanup = stopCleanup

	return loop
}

// GetProvidable returns the AgentProvidable interface implementation.
// This is used by channel managers to access agent capabilities.
func (al *AgentLoop) GetProvidable() channels.AgentProvidable {
	return al.providable
}

// MessageBus returns the unexported bus of the agent loop.
func (al *AgentLoop) MessageBus() *bus.MessageBus {
	return al.bus
}

// SessionManager returns the shared session manager used by all agents.
func (al *AgentLoop) SessionManager() *session.SessionManager {
	if al.registry != nil {
		return al.registry.sharedSessionManager
	}
	return nil
}

// registerSessionCancel delegates to sessionManager.
func (al *AgentLoop) registerSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	return al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
}

// cancelSession delegates to sessionManager.
func (al *AgentLoop) cancelSession(sessionKey string) int {
	return al.sessionManager.CancelSession(sessionKey)
}

// isSessionProcessing returns true if the session is currently being processed
// (the processing semaphore is held). This is used to avoid sending redundant
// subagent.result events when the parent will handle the result via wait_for_subagent.
func (al *AgentLoop) isSessionProcessing(sessionKey string) bool {
	resolvedKey := al.ResolveSessionKey(sessionKey)
	val, ok := al.sessionProcessing.Load(resolvedKey)
	if !ok {
		return false
	}
	ch := val.(chan struct{})
	select {
	case ch <- struct{}{}:
		// Nobody was processing — release immediately
		<-ch
		return false
	default:
		// Channel is full — someone is processing
		return true
	}
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
	if al.stopSessionCleanup != nil {
		al.stopSessionCleanup()
	}
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
