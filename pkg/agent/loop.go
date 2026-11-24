package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/state"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

type InboundDurability interface {
	ShouldSkip(msg bus.InboundMessage) bool
	Finish(msg bus.InboundMessage)
}

type AgentLoop struct {
	bus              *bus.MessageBus
	cfgPtr           atomic.Pointer[config.Config]
	registry         *AgentRegistry
	state            *state.Manager
	running          atomic.Bool
	summarizing      sync.Map
	sessionAliases   sync.Map
	sessionModels    sync.Map
	sessionAgents    sync.Map
	sessionThinking  sync.Map
	sessionOriginsMu sync.Mutex
	sessionOrigins   map[string][]string
	fallback         *providers.FallbackChain
	channelManager   *channels.Manager
	verboseManager   *session.VerboseManager
	sessionKeySeq    atomic.Uint64

	seqSeedMu            sync.Mutex
	seqSeeds             map[string]uint64
	seqSeedPersisted     map[string]uint64
	approvalManager      *channels.ApprovalManager
	sessionProcessing    sync.Map
	subagentSessionAgent sync.Map
	wg                   sync.WaitGroup

	midTurnNotices sync.Map

	inProgressTools sync.Map

	messageProcessor   messageProcessor
	llmRunner          llmRunner
	commandHandler     commandHandler
	sessionManager     sessionManager
	toolCoordinator    toolCoordinator
	groupManager       *group.GroupManager
	goalManager        *GoalManager
	keyringService     *keyring.Service
	redactor           *keyring.Redactor
	dbStore            *store.Store
	providable         *agentProvidableImpl
	stopSessionCleanup func()

	stopOnce sync.Once

	goalStopCtx    context.Context
	goalStopCancel context.CancelFunc

	goalLoopMu       sync.Mutex
	goalLoopSessions map[string]struct{}

	harnessMgrs map[string]*harnessEntry
	harnessMu   sync.Mutex

	inboundDurability InboundDurability
}

func (al *AgentLoop) cfg() *config.Config {
	if cfg := al.cfgPtr.Load(); cfg != nil {
		return cfg
	}
	return config.DefaultConfig()
}

func (al *AgentLoop) ReloadRegistry(cfg *config.Config) {
	if cfg == nil || al.registry == nil {
		return
	}

	al.registry.ReloadAgents(cfg)

	if al.toolCoordinator != nil {
		stopped := al.toolCoordinator.cancelRemovedSubagents(al.registry.ListAgentIDs())
		if stopped > 0 {
			logger.InfoCF("agent", "Cancelled subagents of removed agents during config reload",
				map[string]interface{}{"tasks_stopped": stopped})
		}
	}
	al.attachFolderResolver()
	al.attachSubagentLedgerResolver()
	al.cfgPtr.Store(cfg)

	existingSubagents := al.toolCoordinator.GetSubagents()
	existingBgManagers := al.toolCoordinator.(*toolCoordinatorImpl).bgManagers
	existingMCPManagers := al.toolCoordinator.(*toolCoordinatorImpl).mcpManagers
	updatedSubagents, updatedBgManagers := updateSharedTools(cfg, al.bus, al.registry, al.approvalManager, existingSubagents, existingBgManagers, al.groupManager, al.keyringService)

	for agentID, sm := range updatedSubagents {
		id := agentID
		sm.SetSessionKeyCallback(func(sessionKey, targetAgentID string) {
			if targetAgentID == "" {
				targetAgentID = id
			}
			al.setSubagentSessionAgent(sessionKey, targetAgentID)
			al.persistSubagentSessionAgent(id, sessionKey, targetAgentID)
		})
		sm.SetRegisterSessionCancelCallback(func(sessionKey string, cancel context.CancelFunc) func() {
			return al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
		})
	}

	al.toolCoordinator = newToolCoordinatorWithSubagents(al, updatedSubagents, updatedBgManagers, existingMCPManagers)

	al.toolCoordinator.syncMCPTools()
}

func (al *AgentLoop) UpdateConfigSnapshot(cfg *config.Config) {
	if cfg == nil {
		return
	}
	al.cfgPtr.Store(cfg)
}

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

func (al *AgentLoop) attachFolderResolver() {
	if al.registry == nil {
		return
	}
	al.registry.SetFolderResolver(func(sessionKey string) string {
		resolved := al.ResolveSessionKey(sessionKey)
		if resolved == "" {
			return ""
		}
		agent := al.agentForSession(resolved)
		if agent == nil || agent.Sessions == nil {
			return ""
		}
		return agent.Sessions.GetFolder(resolved)
	})
}

func (al *AgentLoop) attachSubagentLedgerResolver() {
	if al == nil || al.registry == nil || al.toolCoordinator == nil {
		return
	}
	al.registry.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		tc, ok := al.toolCoordinator.(*toolCoordinatorImpl)
		if !ok || tc == nil {
			return ""
		}
		return tc.renderSubagentLedgerDigest(agentID, sessionKey)
	})
}

var subagentTaskIDPattern = regexp.MustCompile(`^subagent-\d+$`)

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
			logger.DebugCF("agent", "GetSubagentParentSessionKey: resolved from task", map[string]interface{}{
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

	if subagentTaskIDPattern.MatchString(taskID) {
		if idx := strings.LastIndex(sessionKey, ":"+taskID); idx > 0 {
			parent := sessionKey[:idx]
			logger.DebugCF("agent", "GetSubagentParentSessionKey: resolved from session key", map[string]interface{}{
				"session_key": sessionKey,
				"task_id":     taskID,
				"parent_key":  parent,
			})
			return parent
		}
	}

	logger.DebugCF("agent", "GetSubagentParentSessionKey: unable to resolve parent", map[string]interface{}{
		"session_key": sessionKey,
		"task_id":     taskID,
	})
	return ""
}

func (al *AgentLoop) startFreshConversation(baseSessionKey, agentID, model string) string {
	baseSessionKey = strings.TrimSpace(baseSessionKey)
	if baseSessionKey == "" {
		return ""
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
					sessionAgent.Sessions.GetOrCreate(baseSessionKey)
					sessionAgent.Sessions.ResetTokenCounts(baseSessionKey)
					sessionAgent.Sessions.TruncateHistory(baseSessionKey, 0)
					sessionAgent.Sessions.SetSummary(baseSessionKey, "")
					sessionAgent.Sessions.SetThinkingLevel(baseSessionKey, "")
				}

				if agentID != "" {
					al.setSessionAgent(baseSessionKey, agentID)
				}
				if model != "" {
					al.sessionModels.Store(baseSessionKey, model)
				}
				al.sessionThinking.Delete(baseSessionKey)
				al.dropSessionTools(baseSessionKey)
				if sessionAgent != nil && sessionAgent.ContextBuilder != nil {
					sessionAgent.ContextBuilder.ClearLoadedSkills(baseSessionKey)
				}

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
	al.setSessionAlias(baseSessionKey, newSessionKey)
	al.dropSessionTools(baseSessionKey)
	if sessionAgent != nil && sessionAgent.ContextBuilder != nil {
		sessionAgent.ContextBuilder.ClearLoadedSkills(baseSessionKey)
	}

	if agentID != "" {
		al.setSessionAgent(newSessionKey, agentID)
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
	SkipGoalLoop    bool
	ModelOverride   string
	AppliedCommand  *appliedCommand

	midTurnQueue *midTurnNoticeQueue
}

type appliedCommand struct {
	Original    string
	Name        string
	Description string
	Args        string
	Source      string
	Agent       string
	Model       string
}

func commandAppliedOf(c *appliedCommand) *providers.CommandApplied {
	if c == nil {
		return nil
	}
	return &providers.CommandApplied{
		Name:        c.Name,
		Description: c.Description,
		Args:        c.Args,
		Agent:       c.Agent,
		Model:       c.Model,
		Source:      c.Source,
	}
}

type SummarizeStats struct {
	BeforeMessages  int
	AfterMessages   int
	DroppedMessages int
	BeforeTokens    int
	AfterTokens     int
	SavedTokens     int
}

func NewAgentLoopWithStore(cfg *config.Config, msgBus *bus.MessageBus, s *store.Store) *AgentLoop {
	// Wire the shared store BEFORE building the registry: NewAgentRegistry
	// eagerly creates agent instances, and their skills usage stores must
	// see the SQLite repo (otherwise they silently start in file mode).
	var dbStore *store.Store
	if s != nil {
		dbStore = s
		logger.InfoC("store", fmt.Sprintf("SQLite store opened"))
		auth.UseStore(dbStore.Auth())
		skills.SetUsageRepo(dbStore.Usage())
		mcp.SetUsageRepo(dbStore.Usage())
	} else {
		// Reset too: a loop without a store must not inherit a repo wired
		// by an earlier loop (possibly already closed) in this process.
		skills.SetUsageRepo(nil)
		mcp.SetUsageRepo(nil)
	}

	registry := NewAgentRegistry(cfg)

	approvalManager := channels.NewApprovalManager()

	keyringSvc := keyring.NewService(keyring.ServiceConfig{
		Enabled:          cfg.Keyring.Enabled,
		VaultPath:        cfg.KeyringVaultPath(),
		Backend:          cfg.Keyring.Backend,
		AuditLogSize:     cfg.Keyring.AuditLogSize,
		AllowAgentSet:    cfg.Keyring.AllowAgentSet,
		AllowAgentDelete: cfg.Keyring.AllowAgentDelete,
		LeleDir:          config.GetLeleDir(),
	})
	redactor := keyring.NewRedactor(keyringSvc)

	cooldown := providers.NewCooldownTracker()
	fallbackChain := providers.NewFallbackChain(cooldown)

	sharedSessionManager := session.NewSessionManager()

	if dbStore != nil {
		sharedSessionManager.SetSessionRepo(dbStore.Sessions())
		sharedSessionManager.SetSessionKV(dbStore.KV())
	}

	legacyDir := filepath.Join(config.GetLeleDir(), "sessions")
	if info, statErr := os.Stat(legacyDir); statErr == nil && info.IsDir() {
		logger.WarnCF("session", "Legacy JSON sessions directory found — ignored (SQLite-only)", map[string]interface{}{
			"path":   legacyDir,
			"action": "run 'lele migrate-storage' if sessions are missing",
		})
	}

	registry.SetSharedSessionManager(sharedSessionManager)

	stopCleanup := sharedSessionManager.StartCleanupGoroutine(5 * time.Minute)

	defaultAgent := registry.GetDefaultAgent()
	var stateManager *state.Manager
	if defaultAgent != nil {
		stateManager = state.NewManager(defaultAgent.Workspace)
	}

	if dbStore != nil && stateManager != nil {
		stateManager.SetKVRepo(dbStore.KV())
	}

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
		keyringService:  keyringSvc,
		redactor:        redactor,
		dbStore:         dbStore,
	}
	loop.goalStopCtx, loop.goalStopCancel = context.WithCancel(context.Background())
	loop.goalLoopSessions = make(map[string]struct{})
	loop.seqSeeds = make(map[string]uint64)
	loop.seqSeedPersisted = make(map[string]uint64)
	loop.cfgPtr.Store(cfg)

	loop.attachFolderResolver()

	loop.messageProcessor = newMessageProcessor(loop)
	loop.llmRunner = newLLMRunner(loop)
	loop.commandHandler = newCommandHandler(loop)
	loop.sessionManager = newSessionManager(loop)

	groupLLMRunner := newLLMRunner(loop)
	resolveAgent := func(agentID string) (group.AgentContext, bool) {
		agent, ok := loop.registry.GetAgent(agentID)
		if !ok || agent == nil {
			return group.AgentContext{}, false
		}
		persona := ""
		if agent.ContextBuilder != nil {
			persona = agent.ContextBuilder.GetInitialContext()
		}
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		return group.AgentContext{
			AgentID:       agent.ID,
			Name:          name,
			Workspace:     agent.Workspace,
			SystemPrompt:  persona,
			ContextWindow: agent.ContextWindow,
			MaxTokens:     agent.MaxTokens,
		}, true
	}
	turnExecutor := func(ctx context.Context, req group.TurnRequest) (string, int, error) {
		return groupLLMRunner.runGroupTurn(ctx, req)
	}
	gm := group.NewGroupManager(resolveAgent, turnExecutor, loop.bus.PublishOutbound)
	gm.SetEnabledHook(loop.GroupsEnabled)
	gm.SetSessionAliasResolver(loop.ResolveSessionKey)
	gm.SetStoreDir(filepath.Join(config.GetLeleDir(), "groups"))
	if dbStore != nil {
		gm.SetStore(dbStore.Groups())
	}
	if _, err := gm.LoadHistorical(); err != nil {
		logger.WarnC("group", fmt.Sprintf("groups: failed to hydrate history: %v", err))
	}
	loop.groupManager = gm

	goalMgr := NewGoalManager(filepath.Join(config.GetLeleDir(), "goals"))
	if dbStore != nil {
		goalMgr.SetStore(dbStore.Goals())
	}
	loop.goalManager = goalMgr

	subagents, bgManagers := registerSharedTools(cfg, msgBus, registry, approvalManager, loop.groupManager, keyringSvc)

	for agentID, sm := range subagents {
		id := agentID
		sm.SetSessionKeyCallback(func(sessionKey, targetAgentID string) {
			if targetAgentID == "" {
				targetAgentID = id
			}
			loop.setSubagentSessionAgent(sessionKey, targetAgentID)
			loop.persistSubagentSessionAgent(id, sessionKey, targetAgentID)
		})
		sm.SetRegisterSessionCancelCallback(func(sessionKey string, cancel context.CancelFunc) func() {
			return loop.sessionManager.RegisterSessionCancel(sessionKey, cancel)
		})
	}

	loop.toolCoordinator = newToolCoordinatorWithSubagents(loop, subagents, bgManagers, nil)

	loop.attachSubagentLedgerResolver()

	loop.toolCoordinator.syncMCPTools()

	if defaultAgent != nil && defaultAgent.Provider != nil {
		judgeModel := defaultAgent.Model
		if judgeModel == "" {
			judgeModel = cfg.Agents.Defaults.Model
		}
		modelResolver := func(sessionKey string) string {
			return loop.sessionManager.ModelForSession(defaultAgent, sessionKey)
		}
		if cfg.Goal.Judge.Mode == "subagent" {
			evaluatorAgentID := cfg.Goal.Judge.Agent
			sm := subagents[evaluatorAgentID]
			if sm == nil {
				sm = subagents[defaultAgent.ID]
			}
			if sm != nil {
				goalMgr.SetJudge(NewSubagentGoalJudge(sm, defaultAgent.Sessions, evaluatorAgentID, "goal", "", 60*time.Second))
			} else {
				logger.WarnCF("agent", "goal.judge.mode=subagent but no subagent manager found; falling back to inline judge", map[string]interface{}{
					"agent": evaluatorAgentID,
				})
				sj := NewSummaryGoalJudge(defaultAgent.Provider, judgeModel, defaultAgent.Sessions, modelResolver)
				sj.SetConfig(cfg)
				goalMgr.SetJudge(sj)
			}
		} else {
			sj := NewSummaryGoalJudge(defaultAgent.Provider, judgeModel, defaultAgent.Sessions, modelResolver)
			sj.SetConfig(cfg)
			goalMgr.SetJudge(sj)
		}
	}

	loop.providable = newAgentProvidable(loop)
	loop.stopSessionCleanup = stopCleanup

	loop.loadDurableSessionState()

	return loop
}

func (al *AgentLoop) GetProvidable() channels.AgentProvidable {
	return al.providable
}

func (al *AgentLoop) MessageBus() *bus.MessageBus {
	return al.bus
}

func (al *AgentLoop) Store() *store.Store {
	return al.dbStore
}

func (al *AgentLoop) GetDefaultAgent() *AgentInstance {
	if al.registry == nil {
		return nil
	}
	return al.registry.GetDefaultAgent()
}

func (al *AgentLoop) GroupManager() *group.GroupManager {
	return al.groupManager
}

func (al *AgentLoop) GroupsEnabled() bool {
	return al.cfg().GroupsFeatureEnabled()
}

func (al *AgentLoop) GoalManager() *GoalManager {
	return al.goalManager
}

func (al *AgentLoop) HandleGoalCommand(sessionKey string, args []string) string {
	if al.commandHandler == nil {
		return "❌ Command handler not initialized."
	}
	impl, ok := al.commandHandler.(*commandHandlerImpl)
	if !ok {
		return "❌ Command handler not available."
	}
	return impl.handleGoalCommand(context.Background(), "native", sessionKey, sessionKey, args)
}

func (al *AgentLoop) KeyringService() *keyring.Service {
	return al.keyringService
}

func (al *AgentLoop) SkillsLoader() *skills.SkillsLoader {
	if al.registry == nil {
		return nil
	}
	agent := al.registry.GetDefaultAgent()
	if agent == nil || agent.ContextBuilder == nil {
		return nil
	}
	return agent.ContextBuilder.skillsLoader
}

func (al *AgentLoop) SkillInstaller() *skills.SkillInstaller {
	if al.registry == nil {
		return nil
	}
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		return nil
	}
	return skills.NewSkillInstaller(agent.Workspace)
}

func (al *AgentLoop) AllGroupSnapshots() []group.GroupSnapshot {
	if al.groupManager == nil {
		return nil
	}
	return al.groupManager.AllSnapshots()
}

func (al *AgentLoop) GroupSnapshotsForSession(sessionKey string) []group.GroupSnapshot {
	if al.groupManager == nil {
		return nil
	}
	return al.groupManager.SnapshotsForSession(sessionKey)
}

func (al *AgentLoop) SessionManager() *session.SessionManager {
	if al.registry != nil {
		return al.registry.sharedSessionManager
	}
	return nil
}

func (al *AgentLoop) registerSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	return al.sessionManager.RegisterSessionCancel(sessionKey, cancel)
}

func (al *AgentLoop) cancelSession(sessionKey string) int {
	return al.sessionManager.CancelSession(sessionKey)
}

func (al *AgentLoop) isSessionProcessing(sessionKey string) bool {
	resolvedKey := al.ResolveSessionKey(sessionKey)
	val, ok := al.sessionProcessing.Load(resolvedKey)
	if !ok {
		return false
	}
	ch := val.(chan struct{})
	select {
	case ch <- struct{}{}:
		<-ch
		return false
	default:
		return true
	}
}

func (al *AgentLoop) publishTurnEnd(m bus.InboundMessage) {
	if m.Channel == "" || constants.IsInternalChannel(m.Channel) {
		return
	}

	msg := bus.OutboundMessage{
		Channel:        m.Channel,
		ChatID:         m.ChatID,
		Event:          "turn.end",
		IsIntermediate: false,
	}
	if m.Metadata != nil && m.Metadata["message_id"] != "" {
		msg.Metadata = map[string]string{"message_id": m.Metadata["message_id"]}
	}

	logger.DebugCF("agent", "Published turn.end", map[string]interface{}{
		"channel": msg.Channel,
		"chat_id": msg.ChatID,
	})
	al.bus.PublishOutbound(msg)
}

func (al *AgentLoop) SetInboundDurability(d InboundDurability) {
	al.inboundDurability = d
}

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

				defer al.publishTurnEnd(m)

				dur := al.inboundDurability
				if dur != nil && dur.ShouldSkip(m) {
					logger.InfoCF("durable", "Skipping already-processed inbound replay",
						map[string]interface{}{
							"channel":     m.Channel,
							"chat_id":     m.ChatID,
							"session_key": m.SessionKey,
							"spool_id":    m.SpoolID,
						})
					return
				}

				finishTurn := true
				if dur != nil {
					defer func() {
						if !finishTurn {
							return
						}
						dur.Finish(m)
					}()
				}

				if m.Metadata == nil {
					m.Metadata = make(map[string]string, 1)
				}

				response, err := al.messageProcessor.processMessage(ctx, m)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						if !al.running.Load() {
							finishTurn = false
							logger.WarnCF("durable", "Inbound turn cancelled by shutdown; spool row left for replay",
								map[string]interface{}{
									"channel":     m.Channel,
									"chat_id":     m.ChatID,
									"session_key": m.SessionKey,
									"spool_id":    m.SpoolID,
								})
						}
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
					if m.Metadata != nil && m.Metadata[llmErrorMetadataKey] == "true" {
						outboundMsg.Metadata = map[string]string{"error": "true"}
					}
					al.bus.PublishOutbound(outboundMsg)
				}
			}(msg)
		}
	}

	return nil
}

func (al *AgentLoop) Shutdown(ctx context.Context) error {
	al.running.Store(false)

	drained := make(chan struct{})
	go func() {
		al.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		logger.InfoC("agent", "Agent loop drained: no turn in flight at shutdown")
		if err := al.CloseMCPManagers(); err != nil {
			logger.WarnCF("agent", "Failed to close MCP managers during shutdown",
				map[string]interface{}{"error": err.Error()})
		}
		return nil
	case <-ctx.Done():
		logger.WarnCF("agent", "Agent loop drain timed out", map[string]interface{}{
			"error": ctx.Err().Error(),
		})
		return ctx.Err()
	}
}

func (al *AgentLoop) Stop() {
	_ = al.StopWithin(0)
}

func (al *AgentLoop) StopWithin(grace time.Duration) error {
	al.running.Store(false)

	abandoned := false
	var deadline time.Time
	if grace > 0 {
		deadline = time.Now().Add(grace)
	}
	remaining := func() (time.Duration, bool) {
		if deadline.IsZero() {
			return 0, true
		}
		left := time.Until(deadline)
		return left, left > 0
	}
	abandon := func(stage string) {
		abandoned = true
		logger.WarnCF("agent", "Teardown budget spent; abandoning teardown",
			map[string]interface{}{"stage": stage, "grace": grace.String()})
	}
	al.stopOnce.Do(func() {
		if al.goalStopCancel != nil {
			al.goalStopCancel()
		}
		if al.stopSessionCleanup != nil {
			al.stopSessionCleanup()
		}

		if al.toolCoordinator != nil {
			for _, sm := range al.toolCoordinator.GetSubagents() {
				if sm == nil {
					continue
				}
				left, ok := remaining()
				if !ok {
					abandon("retention sweepers")
					return
				}
				if err := sm.StopRetentionCleanupWithin(left); err != nil {
					abandoned = true
					logger.WarnCF("agent", "Subagent retention sweeper did not stop; abandoning teardown",
						map[string]interface{}{"error": err.Error()})
					return
				}
			}
		}

		if grace > 0 {
			done := make(chan struct{})
			go func() {
				al.wg.Wait()
				close(done)
			}()
			left, ok := remaining()
			if !ok {
				abandon("in-flight turns")
				return
			}
			select {
			case <-done:
			case <-time.After(left):
				abandoned = true
				logger.WarnCF("agent", "Agent loop stop timed out; abandoning in-flight turns",
					map[string]interface{}{"grace": grace.String()})
				return
			}
		} else {
			al.wg.Wait()
		}

		if al.groupManager != nil {
			left, ok := remaining()
			if !ok {
				abandon("group run drain")
				return
			}
			drained := al.groupManager.DrainAllRunGoroutines(left)
			if !drained {
				abandoned = true
				logger.WarnCF("agent", "Group run drain timed out; abandoning in-flight group runs — terminal state may be lost",
					map[string]interface{}{"grace": grace.String()})
				return
			}
		}

		if al.dbStore != nil {
			if err := al.dbStore.Close(); err != nil {
				logger.ErrorC("store", fmt.Sprintf("Failed to close SQLite store: %v", err))
			}
		}
	})

	if abandoned {
		return fmt.Errorf("agent loop stop timed out after %s; teardown abandoned with work in flight", grace)
	}
	return nil
}

func (al *AgentLoop) markGoalLoopActive(sessionKey string) {
	al.goalLoopMu.Lock()
	defer al.goalLoopMu.Unlock()
	if al.goalLoopSessions == nil {
		al.goalLoopSessions = make(map[string]struct{})
	}
	al.goalLoopSessions[sessionKey] = struct{}{}
}

func (al *AgentLoop) clearGoalLoopActive(sessionKey string) {
	al.goalLoopMu.Lock()
	defer al.goalLoopMu.Unlock()
	delete(al.goalLoopSessions, sessionKey)
}

func (al *AgentLoop) isGoalLoopActive(sessionKey string) bool {
	al.goalLoopMu.Lock()
	defer al.goalLoopMu.Unlock()
	_, ok := al.goalLoopSessions[sessionKey]
	return ok
}

func (al *AgentLoop) SetChannelManager(cm *channels.Manager) {
	al.channelManager = cm
}

func (al *AgentLoop) SetApprovalManager(am *channels.ApprovalManager) {
	al.approvalManager = am
}

func (al *AgentLoop) GetApprovalManager() *channels.ApprovalManager {
	return al.approvalManager
}

func (al *AgentLoop) SetExecWhitelist(commands []string) {
	if al.registry == nil {
		return
	}
	al.registry.SetExecWhitelist(commands)
}

func (al *AgentLoop) RecordLastChannel(channel string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChannel(channel)
}

func (al *AgentLoop) RecordLastChatID(chatID string) error {
	if al.state == nil {
		return nil
	}
	return al.state.SetLastChatID(chatID)
}

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
	if agentID, ok := al.subagentSessionAgent.Load(sessionKey); ok {
		result := agentID.(string)
		logger.DebugCF("agent", "getSessionAgent found in subagentSessionAgent", map[string]interface{}{
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

func (al *AgentLoop) sessionAgentOverride(sessionKey string) (string, bool) {
	sessionKey = al.ResolveSessionKey(sessionKey)
	if v, ok := al.sessionAgents.Load(sessionKey); ok {
		if agentID, isString := v.(string); isString && agentID != "" {
			return agentID, true
		}
	}
	if v, ok := al.subagentSessionAgent.Load(sessionKey); ok {
		if agentID, isString := v.(string); isString && agentID != "" {
			return agentID, true
		}
	}
	return "", false
}

func (al *AgentLoop) getDefaultAgentID() string {
	if defaultAgent := al.registry.GetDefaultAgent(); defaultAgent != nil {
		return defaultAgent.ID
	}
	return "main"
}

func (al *AgentLoop) getSessionContextWindow(sessionKey string) int {
	resolvedSessionKey := al.ResolveSessionKey(sessionKey)
	agent := al.agentForSession(resolvedSessionKey)
	if agent == nil {
		return 128000
	}

	model := agent.Model
	if m, ok := al.sessionModels.Load(resolvedSessionKey); ok {
		if selected, ok := m.(string); ok && selected != "" {
			model = selected
		}
	}

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

func (al *AgentLoop) RegisterTool(tool tools.Tool) {
	al.toolCoordinator.RegisterTool(tool)
}

func (al *AgentLoop) GetSubagents() map[string]*tools.SubagentManager {
	return al.toolCoordinator.GetSubagents()
}

func (al *AgentLoop) GetStartupInfo() map[string]interface{} {
	return al.toolCoordinator.GetStartupInfo()
}

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

func (al *AgentLoop) dropSessionTools(sessionKey string) {
	if al == nil || al.toolCoordinator == nil {
		return
	}
	al.toolCoordinator.dropSessionTools(sessionKey)
}

func (al *AgentLoop) resetAgentSession(agent *AgentInstance, sessionKey string) error {
	previousHistory := agent.Sessions.GetHistory(sessionKey)
	previousSummary := agent.Sessions.GetSummary(sessionKey)
	agent.Sessions.TruncateHistory(sessionKey, 0)
	agent.Sessions.SetSummary(sessionKey, "")
	agent.Sessions.ResetTokenCounts(sessionKey)
	al.sessionModels.Delete(sessionKey)
	al.sessionThinking.Delete(sessionKey)
	al.dropSessionTools(sessionKey)
	if agent.ContextBuilder != nil {
		agent.ContextBuilder.ClearLoadedSkills(sessionKey)
	}
	if agent.Sessions != nil {
		agent.Sessions.SetThinkingLevel(sessionKey, "")
		agent.Sessions.SetModel(sessionKey, "")
	}
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
