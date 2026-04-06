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
	"sort"
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
	bus              *bus.MessageBus
	cfgPtr           atomic.Pointer[config.Config]
	registry         *AgentRegistry
	state            *state.Manager
	running          atomic.Bool
	summarizing      sync.Map
	sessionAliases   sync.Map // base session key -> active session key
	sessionModels    sync.Map
	sessionAgents    sync.Map // sessionKey -> agentID for agent switching
	sessionThinking  sync.Map // sessionKey -> reasoning effort ("off", "low", "medium", "high")
	fallback         *providers.FallbackChain
	channelManager   *channels.Manager
	subagents        map[string]*tools.SubagentManager
	verboseManager   *session.VerboseManager
	sessionCancels   sync.Map // sessionKey -> context.CancelFunc
	sessionKeySeq    atomic.Uint64
	sessionCancelSeq atomic.Uint64
	approvalManager  *channels.ApprovalManager // Manager for command approvals

	// Internal components (delegated operations)
	messageProcessor messageProcessor
	llmRunner        llmRunner
	commandHandler   commandHandler
	sessionManager   sessionManager
	toolCoordinator  toolCoordinator
}

func (al *AgentLoop) cfg() *config.Config {
	if cfg := al.cfgPtr.Load(); cfg != nil {
		return cfg
	}
	return config.DefaultConfig()
}

func (al *AgentLoop) UpdateConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	al.cfgPtr.Store(cfg)
}

func (al *AgentLoop) resolveSessionKey(sessionKey string) string {
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

	newSessionKey := al.nextConversationSessionKey(baseSessionKey)
	al.sessionAliases.Store(baseSessionKey, newSessionKey)

	if agentID != "" {
		al.sessionAgents.Store(newSessionKey, agentID)
	}
	if model != "" {
		al.sessionModels.Store(newSessionKey, model)
	}
	al.sessionThinking.Delete(newSessionKey)

	// Reset token counts for the new session to ensure clean state.
	// Get the agent for this session to access its session manager.
	var sessionAgent *AgentInstance
	if agentID != "" {
		if a, ok := al.registry.GetAgent(agentID); ok {
			sessionAgent = a
		}
	}
	if sessionAgent == nil {
		sessionAgent = al.registry.GetDefaultAgent()
	}
	if sessionAgent != nil {
		// GetOrCreate ensures the session exists before resetting tokens.
		sessionAgent.Sessions.GetOrCreate(newSessionKey)
		sessionAgent.Sessions.ResetTokenCounts(newSessionKey)
		// Clear any existing history to ensure a truly fresh conversation.
		sessionAgent.Sessions.TruncateHistory(newSessionKey, 0)
		// Also clear any summary from previous sessions.
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
}

type sessionCancelGroup struct {
	mu      sync.Mutex
	cancels map[uint64]context.CancelFunc
}

func newSessionCancelGroup() *sessionCancelGroup {
	return &sessionCancelGroup{
		cancels: make(map[uint64]context.CancelFunc),
	}
}

func (scg *sessionCancelGroup) add(id uint64, cancel context.CancelFunc) {
	scg.mu.Lock()
	defer scg.mu.Unlock()
	scg.cancels[id] = cancel
}

func (scg *sessionCancelGroup) remove(id uint64) bool {
	scg.mu.Lock()
	defer scg.mu.Unlock()
	delete(scg.cancels, id)
	return len(scg.cancels) == 0
}

func (scg *sessionCancelGroup) cancelAll() int {
	scg.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(scg.cancels))
	for id, cancel := range scg.cancels {
		delete(scg.cancels, id)
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	scg.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	return len(cancels)
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

	// Register shared tools to all agents (each agent uses its own provider)
	subagents := registerSharedTools(cfg, msgBus, registry, approvalManager)

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
		subagents:       subagents,
		verboseManager:  verboseManager,
		approvalManager: approvalManager,
	}
	loop.cfgPtr.Store(cfg)

	// Initialize internal components
	loop.messageProcessor = newMessageProcessor(loop)
	loop.llmRunner = newLLMRunner(loop)
	loop.commandHandler = newCommandHandler(loop)
	loop.sessionManager = newSessionManager(loop)
	loop.toolCoordinator = newToolCoordinator(loop)

	return loop
}

func (al *AgentLoop) registerSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	if sessionKey == "" || cancel == nil {
		return func() {}
	}

	id := al.sessionCancelSeq.Add(1)
	rawGroup, _ := al.sessionCancels.LoadOrStore(sessionKey, newSessionCancelGroup())
	group, ok := rawGroup.(*sessionCancelGroup)
	if !ok || group == nil {
		group = newSessionCancelGroup()
		al.sessionCancels.Store(sessionKey, group)
	}
	group.add(id, cancel)

	return func() {
		cancel()
		if !group.remove(id) {
			return
		}
		if current, ok := al.sessionCancels.Load(sessionKey); ok && current == group {
			al.sessionCancels.Delete(sessionKey)
		}
	}
}

func (al *AgentLoop) cancelSession(sessionKey string) int {
	if sessionKey == "" {
		return 0
	}

	rawGroup, ok := al.sessionCancels.Load(sessionKey)
	if !ok {
		return 0
	}

	switch entry := rawGroup.(type) {
	case *sessionCancelGroup:
		stopped := entry.cancelAll()
		al.sessionCancels.Delete(sessionKey)
		return stopped
	case context.CancelFunc:
		if entry != nil {
			entry()
		}
		al.sessionCancels.Delete(sessionKey)
		return 1
	default:
		al.sessionCancels.Delete(sessionKey)
		return 0
	}
}

// registerSharedTools registers tools that are shared across all agents (web, message, spawn).
// Each agent uses its own provider for subagent spawning.
func registerSharedTools(cfg *config.Config, msgBus *bus.MessageBus, registry *AgentRegistry, approvalManager *channels.ApprovalManager) map[string]*tools.SubagentManager {
	subagents := make(map[string]*tools.SubagentManager)
	for _, agentID := range registry.ListAgentIDs() {
		agent, ok := registry.GetAgent(agentID)
		if !ok {
			continue
		}

		// Web tools
		if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
			BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
			BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
			BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
			DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
			DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
			PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
			PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
			PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
		}); searchTool != nil {
			agent.Tools.Register(searchTool)
		}
		agent.Tools.Register(tools.NewWebFetchTool(50000))

		// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
		agent.Tools.Register(tools.NewI2CTool())
		agent.Tools.Register(tools.NewSPITool())

		// File tool
		sendFileTool := tools.NewSendFileTool()
		sendFileTool.SetSendCallback(func(channel, chatID string, payload tools.SendFilePayload) error {
			msgBus.PublishOutbound(bus.OutboundMessage{
				Channel:     channel,
				ChatID:      chatID,
				Content:     payload.Content,
				Attachments: payload.Attachments,
			})
			return nil
		})
		agent.Tools.Register(sendFileTool)

		// Shell/Exec tool with approval support
		execTool := tools.NewExecToolWithConfig(agent.Workspace, cfg.Agents.Defaults.RestrictToWorkspace, cfg)
		if approvalManager != nil {
			execTool.SetApprovalMode(true)
		}
		agent.Tools.Register(execTool)

		// Spawn tool with allowlist checker - use agent's own provider
		subagentManager := tools.NewSubagentManager(agent.Provider, agent.Model, agent.Workspace, msgBus)
		subagentManager.SetLLMOptions(agent.MaxTokens, agent.Temperature)
		subagentManager.SetMaxIterations(agent.MaxIterations)
		// Set callback to get context for specific agent types (each agent loads its own AGENT.md, SOUL.md, etc.)
		subagentManager.SetAgentContextCallback(func(agentID string) tools.AgentContextInfo {
			if targetAgent, ok := registry.GetAgent(agentID); ok {
				return tools.AgentContextInfo{
					Context:   targetAgent.ContextBuilder.GetInitialContext(),
					Workspace: targetAgent.Workspace,
					Name:      targetAgent.Name,
					Model:     targetAgent.Model,
					Provider:  targetAgent.Provider,
				}
			}
			// Fallback: use parent agent's context if agent not found
			return tools.AgentContextInfo{
				Context:   agent.ContextBuilder.GetInitialContext(),
				Workspace: agent.Workspace,
				Name:      agent.Name,
				Model:     agent.Model,
				Provider:  agent.Provider,
			}
		})
		spawnTool := tools.NewSpawnTool(subagentManager)
		subagents[agentID] = subagentManager
		currentAgentID := agentID
		spawnTool.SetAllowlistChecker(func(targetAgentID string) bool {
			return registry.CanSpawnSubagent(currentAgentID, targetAgentID)
		})
		agent.Tools.Register(spawnTool)
		subagentManager.SetTools(agent.Tools.CloneWithout("send_file")) // Subagents inherit all parent tools except direct external file delivery

		// Update context builder with the complete tools registry
		agent.ContextBuilder.SetToolsRegistry(agent.Tools)
	}
	return subagents
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

			response, err := al.messageProcessor.processMessage(ctx, msg)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					logger.InfoCF("agent", "Message processing canceled",
						map[string]interface{}{
							"channel":     msg.Channel,
							"chat_id":     msg.ChatID,
							"session_key": msg.SessionKey,
						})
					continue
				}
				response = fmt.Sprintf("Error processing message: %v", err)
			}

			if response != "" {
				outboundMsg := bus.OutboundMessage{
					Channel: msg.Channel,
					ChatID:  msg.ChatID,
					Content: response,
				}
				if msg.Metadata != nil && msg.Metadata["message_id"] != "" {
					outboundMsg.MessageID = msg.Metadata["message_id"]
					outboundMsg.ReplyTo = msg.Metadata["message_id"]
				}
				al.bus.PublishOutbound(outboundMsg)
			}
		}
	}

	return nil
}

// Stop stops the agent loop.
func (al *AgentLoop) Stop() {
	al.running.Store(false)
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

// RegisterTool registers a tool to all agents.
func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	for _, agentID := range al.registry.ListAgentIDs() {
		if agent, ok := al.registry.GetAgent(agentID); ok {
			agent.Tools.Register(tool)
		}
	}
}

// ============================================================================
// AgentProvidable Interface Implementation
// ============================================================================

// GetAgentInfo returns basic agent info for the UI (implements AgentProvidable).
func (al *AgentLoop) GetAgentInfo(agentID string) (channels.AgentBasicInfo, bool) {
	agent, ok := al.registry.GetAgent(agentID)
	if !ok {
		return channels.AgentBasicInfo{}, false
	}
	return channels.AgentBasicInfo{
		ID:            agent.ID,
		Name:          agent.Name,
		Model:         agent.Model,
		Workspace:     agent.Workspace,
		MaxIterations: agent.MaxIterations,
		MaxTokens:     agent.MaxTokens,
		Temperature:   agent.Temperature,
		Fallbacks:     agent.Fallbacks,
		SkillsFilter:  agent.SkillsFilter,
	}, true
}

func (al *AgentLoop) agentForSession(sessionKey string) *AgentInstance {
	resolvedSessionKey := al.resolveSessionKey(sessionKey)
	agent := al.registry.GetDefaultAgent()
	if selectedAgentID := al.GetSessionAgent(resolvedSessionKey); selectedAgentID != "" {
		if selectedAgent, ok := al.registry.GetAgent(selectedAgentID); ok {
			agent = selectedAgent
		}
	}
	return agent
}

// GetSessionHistory returns the persisted history for a session (implements AgentProvidable).
func (al *AgentLoop) GetSessionHistory(sessionKey string) []providers.Message {
	resolvedSessionKey := al.resolveSessionKey(sessionKey)
	agent := al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return nil
	}
	return agent.Sessions.GetHistory(resolvedSessionKey)
}

// GetSessionModel returns the effective model for a session (implements AgentProvidable).
func (al *AgentLoop) GetSessionModel(sessionKey string) string {
	resolvedSessionKey := al.resolveSessionKey(sessionKey)
	agent := al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return ""
	}
	if model, ok := al.sessionModels.Load(resolvedSessionKey); ok {
		if selected, ok := model.(string); ok && selected != "" {
			return selected
		}
	}
	return agent.Model
}

// SetSessionModel sets the model for a session (implements AgentProvidable).
func (al *AgentLoop) SetSessionModel(sessionKey, model string) string {
	resolvedSessionKey := al.resolveSessionKey(sessionKey)
	if resolvedSessionKey == "" {
		return ""
	}
	next := al.cfg().Providers.ResolveModelAlias(model, al.cfg().Agents.Defaults.Provider)
	al.sessionModels.Store(resolvedSessionKey, next)
	return next
}

// ListAvailableModels returns configured model aliases for the provider backing an agent (implements AgentProvidable).
func (al *AgentLoop) ListAvailableModels(agentID string) []string {
	providerName := al.cfg().Agents.Defaults.Provider
	if agentID != "" {
		if agent, ok := al.registry.GetAgent(agentID); ok && agent != nil {
			if ref := providers.ParseModelRef(agent.Model, al.cfg().Agents.Defaults.Provider); ref != nil {
				providerName = ref.Provider
			}
		}
	}

	provider, ok := al.cfg().Providers.GetNamed(providerName)
	if !ok || len(provider.Models) == 0 {
		return nil
	}

	models := make([]string, 0, len(provider.Models))
	for alias := range provider.Models {
		models = append(models, alias)
	}
	sort.Strings(models)
	return models
}

// GetConfigSnapshot returns the current configuration snapshot (implements AgentProvidable).
func (al *AgentLoop) GetConfigSnapshot() *config.Config {
	return al.cfg()
}

// ListAvailableAgentIDs returns the list of available agent IDs (implements AgentProvidable).
func (al *AgentLoop) ListAvailableAgentIDs() []string {
	return al.registry.ListAgentIDs()
}

// SetSessionAgent sets the active agent for a specific session (implements AgentProvidable).
func (al *AgentLoop) SetSessionAgent(sessionKey, agentID string) {
	al.sessionAgents.Store(al.resolveSessionKey(sessionKey), agentID)
}

// GetSessionAgent gets the active agent for a session (implements AgentProvidable).
func (al *AgentLoop) GetSessionAgent(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	if agentID, ok := al.sessionAgents.Load(sessionKey); ok {
		return agentID.(string)
	}
	// Return default agent
	if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
		return defaultAgent.ID
	}
	return "main"
}

// GetDefaultAgentID returns the default agent ID (implements AgentProvidable).
func (al *AgentLoop) GetDefaultAgentID() string {
	if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
		return defaultAgent.ID
	}
	return "main"
}

// GetStatus returns the current status for a session (implements AgentProvidable).
func (al *AgentLoop) GetStatus(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}
	// Delegate to message processor for formatting
	if mp, ok := al.messageProcessor.(*messageProcessorImpl); ok {
		return mp.formatStatusResponse(agent, sessionKey, "telegram")
	}
	return "No default agent configured."
}

// StopAgent stops the agent processing for a session (implements AgentProvidable).
func (al *AgentLoop) StopAgent(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	subagentCount := 0
	if al.toolCoordinator != nil {
		subagentCount = al.toolCoordinator.stopAllSubagents()
	}
	al.cancelSession(sessionKey)
	if subagentCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d subagente(s)).", subagentCount)
	}
	return "⏹️ Agente detenido."
}

// CompactSession compacts the session history (implements AgentProvidable).
func (al *AgentLoop) CompactSession(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured."
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) <= 4 {
		return "📭 Not enough messages to compact (need 5+)."
	}

	stats := al.sessionManager.summarizeSession(agent, sessionKey)
	if stats == nil {
		return "❌ Compaction failed or nothing to compact."
	}

	return fmt.Sprintf("✅ Compacted session: %d messages → %d messages (%d tokens saved)",
		stats.BeforeMessages, stats.AfterMessages, stats.SavedTokens)
}

func (al *AgentLoop) resetAgentSession(agent *AgentInstance, sessionKey string) error {
	previousHistory := agent.Sessions.GetHistory(sessionKey)
	previousSummary := agent.Sessions.GetSummary(sessionKey)
	agent.Sessions.TruncateHistory(sessionKey, 0)
	agent.Sessions.SetSummary(sessionKey, "")
	agent.Sessions.ResetTokenCounts(sessionKey)
	agent.ContextBuilder.ResetMemoryContext()
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

// ToggleVerbose toggles verbose mode for a session (implements AgentProvidable).
func (al *AgentLoop) ToggleVerbose(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "Verbose mode requires a session context. Please start a conversation first."
	}
	newLevel := al.verboseManager.CycleLevel(sessionKey)
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

// GetVerboseLevel returns the current verbose level for a session (implements AgentProvidable).
func (al *AgentLoop) GetVerboseLevel(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "off"
	}
	return string(al.verboseManager.GetLevel(sessionKey))
}

// SetVerboseLevel sets the verbose level for a session (implements AgentProvidable).
func (al *AgentLoop) SetVerboseLevel(sessionKey string, level string) bool {
	sessionKey = al.resolveSessionKey(sessionKey)
	if sessionKey == "" {
		return false
	}
	if !session.IsValidVerboseLevel(level) {
		return false
	}
	al.verboseManager.SetLevel(sessionKey, session.VerboseLevel(level))
	return true
}

// GetThinkLevel returns the current reasoning effort level for a session (implements AgentProvidable).
func (al *AgentLoop) GetThinkLevel(sessionKey string) string {
	sessionKey = al.resolveSessionKey(sessionKey)
	if sessionKey == "" {
		return "default"
	}
	if v, ok := al.sessionThinking.Load(sessionKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "default"
}

// SetThinkLevel sets the reasoning effort level for a session (implements AgentProvidable).
func (al *AgentLoop) SetThinkLevel(sessionKey string, level string) bool {
	sessionKey = al.resolveSessionKey(sessionKey)
	if sessionKey == "" {
		return false
	}
	validLevels := map[string]bool{"off": true, "low": true, "medium": true, "high": true}
	if !validLevels[level] {
		return false
	}
	if level == "off" {
		al.sessionThinking.Delete(sessionKey)
	} else {
		al.sessionThinking.Store(sessionKey, level)
	}
	return true
}

// GetSubagents lists running subagents (implements AgentProvidable).
func (al *AgentLoop) GetSubagents() string {
	return formatSubagentTaskList(al.toolCoordinator.listRunningSubagentTasks())
}

// ClearSession starts a fresh conversation for the current chat (implements AgentProvidable).
// It preserves the selected agent while switching the chat to a new empty session.
func (al *AgentLoop) ClearSession(sessionKey string) string {
	baseSessionKey := strings.TrimSpace(sessionKey)
	sessionKey = al.resolveSessionKey(sessionKey)
	agent := al.agentForSession(sessionKey)
	if agent == nil {
		return "No default agent configured"
	}
	agentModel := agent.Model
	if agentModel == "" {
		agentModel = al.cfg().Agents.Defaults.Model
	}
	if baseSessionKey == "" {
		baseSessionKey = sessionKey
	}
	al.startFreshConversation(baseSessionKey, agent.ID, agentModel)
	return "🔄 New conversation started. Context refreshed from AGENT.md, SOUL.md, USER.md, IDENTITY.md, and MEMORY.md."
}

// ============================================================================
// Public Methods for External Access (delegated to internal components)
// ============================================================================

// GetStartupInfo returns information about loaded tools and skills for logging.
func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	return al.toolCoordinator.GetStartupInfo()
}

// ProcessDirect processes a message directly without going through the message bus.
func (al *AgentLoop) ProcessDirect(ctx context.Context, content, sessionKey string) (string, error) {
	if mp, ok := al.messageProcessor.(*messageProcessorImpl); ok {
		return mp.ProcessDirect(ctx, content, sessionKey)
	}
	return "", fmt.Errorf("message processor not available")
}

// ProcessDirectWithChannel processes a message directly with channel information.
func (al *AgentLoop) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	if mp, ok := al.messageProcessor.(*messageProcessorImpl); ok {
		return mp.ProcessDirectWithChannel(ctx, content, sessionKey, channel, chatID)
	}
	return "", fmt.Errorf("message processor not available")
}

// ProcessHeartbeat processes a heartbeat request without session history.
// Each heartbeat is independent and doesn't accumulate context.
func (al *AgentLoop) ProcessHeartbeat(ctx context.Context, content, channel, chatID string) (string, error) {
	if mp, ok := al.messageProcessor.(*messageProcessorImpl); ok {
		return mp.ProcessHeartbeat(ctx, content, channel, chatID)
	}
	return "", fmt.Errorf("message processor not available")
}
