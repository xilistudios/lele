package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/update"
	"github.com/xilistudios/lele/pkg/utils"
)

const (
	ChannelName = "native"
)

type NativeChannel struct {
	base            *BaseChannel
	cfg             *config.NativeConfig
	auth            *AuthManager
	bus             *bus.MessageBus
	agentLoop       AgentProvidable
	approvalManager *ApprovalManager
	running         bool
	wsClients       map[string]*WSClient
	restStreams     map[string]*restStreamSubscriber

	loopCtx            context.Context
	loopCancel         context.CancelFunc
	activeLoops        atomic.Int64
	authReconcileEvery time.Duration
	leleDir            string
	configPath         string
	mu                 sync.RWMutex
	startTime          time.Time
	pinLimiter         *rateLimiter
	pairLimiter        *rateLimiter
	refreshLimiter     *rateLimiter
	apiLimiter         *rateLimiter
	wsMessageLimiter   *rateLimiter
	authLogLimiter     *rateLimiter
	skillsLoader       *skills.SkillsLoader
	skillInstaller     *skills.SkillInstaller
	workspacePath      string
	reloadConfig       func() error
	reloadMCP          func()
	cronService        CronProvidable
	keyringService     *keyring.Service
	updateService      *update.Updater
	localesMgr         *locales.Manager

	outboundFlusher PeerFlusher

	subagentsCacheMu sync.Mutex
	subagentsCached  *subagentsCache

	mcpAuthFlows     *mcpAuthFlows
	mcpAuthFlowsOnce sync.Once
}

type CronProvidable interface {
	ListJobs(includeDisabled bool) []cron.CronJob
	GetJob(jobID string) *cron.CronJob
	AddJob(name string, schedule cron.CronSchedule, message string, deliver bool, channel, to string) (*cron.CronJob, error)
	UpdateJob(job *cron.CronJob) error
	RemoveJob(jobID string) bool
	EnableJob(jobID string, enabled bool) *cron.CronJob
	RunJobNow(jobID string) error
	Status() map[string]interface{}
}

func (n *NativeChannel) SetCronService(cs CronProvidable) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cronService = cs
}

func (n *NativeChannel) SetKeyringService(ks *keyring.Service) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.keyringService = ks
}

func (n *NativeChannel) SetReloadConfig(fn func() error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reloadConfig = fn
}

func (n *NativeChannel) SetReloadMCP(fn func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reloadMCP = fn
}

func (nc *NativeChannel) RegisterDesktopClient(token, refreshToken string) error {
	if nc == nil || nc.auth == nil {
		return fmt.Errorf("native channel not initialized")
	}
	return nc.auth.RegisterDesktopClient(token, refreshToken)
}

type WSClient struct {
	ID            string
	Conn          *websocket.Conn
	ClientInfo    *ClientInfo
	SessionKey    string
	Subscriptions map[string]bool
	SendChan      chan []byte
	closed        bool

	doneClosed func() bool

	reconnecting   bool
	disconnectedAt time.Time
	pendingMsgs    []json.RawMessage
	maxPendingMsgs int
	reconnectTimer *time.Timer
	mu             sync.Mutex

	done chan struct{}

	activeWriteLoops atomic.Int64
}

func rateOr(value, def int) int {
	if value <= 0 {
		return def
	}
	return value
}

func IsLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if len(host) > 2 && host[0] == '[' && host[len(host)-1] == ']' {
		host = host[1 : len(host)-1]
	}
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func NewNativeChannel(cfg *config.Config, messageBus *bus.MessageBus, agentLoop AgentProvidable, approvalManager *ApprovalManager) (*NativeChannel, error) {
	nativeCfg := cfg.Channels.Native

	if nativeCfg.LeleDir == "" {
		nativeCfg.LeleDir = config.GetLeleDir()
	}
	leleDir := nativeCfg.LeleDir

	auth, err := NewAuthManager(&nativeCfg, leleDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	base := NewBaseChannel(ChannelName, nativeCfg, messageBus, []string{})

	var pinLimiter, pairLimiter, refreshLimiter, apiLimiter, wsMessageLimiter *rateLimiter
	if rl := nativeCfg.RateLimit; rl.Enabled {
		pinLimiter = newRateLimiter(rateOr(rl.PinPerMinute, config.DefaultNativeRateLimitPinPerMinute), time.Minute)
		pairLimiter = newRateLimiter(rateOr(rl.PairPerMinute, config.DefaultNativeRateLimitPairPerMinute), time.Minute)
		refreshLimiter = newRateLimiter(rateOr(rl.RefreshPerMinute, config.DefaultNativeRateLimitRefreshPerMinute), time.Minute)
		apiLimiter = newRateLimiter(rateOr(rl.APIPerMinute, config.DefaultNativeRateLimitAPIPerMinute), time.Minute)
		wsMessageLimiter = newRateLimiter(rateOr(rl.WSMessagesPerMinute, config.DefaultNativeRateLimitWSMessagesPerMinute), time.Minute)
	}
	authLogLimiter := newRateLimiter(6, time.Minute)

	workspacePath := cfg.WorkspacePath()
	globalSkillsDir := filepath.Join(leleDir, "skills")
	builtinSkillsDir := filepath.Join(leleDir, "lele", "skills")
	directorySkillsDir := ""
	if wd, err := os.Getwd(); err == nil {
		directorySkillsDir = filepath.Join(wd, ".lele", "skills")
	}

	skillsLoader := skills.NewSkillsLoader(workspacePath, globalSkillsDir, builtinSkillsDir, directorySkillsDir)
	skillInstaller := skills.NewSkillInstaller(workspacePath)

	return &NativeChannel{
		base:             base,
		cfg:              &nativeCfg,
		auth:             auth,
		bus:              messageBus,
		agentLoop:        agentLoop,
		approvalManager:  approvalManager,
		wsClients:        make(map[string]*WSClient),
		restStreams:      make(map[string]*restStreamSubscriber),
		leleDir:          leleDir,
		pinLimiter:       pinLimiter,
		pairLimiter:      pairLimiter,
		refreshLimiter:   refreshLimiter,
		apiLimiter:       apiLimiter,
		wsMessageLimiter: wsMessageLimiter,
		authLogLimiter:   authLogLimiter,
		skillsLoader:     skillsLoader,
		skillInstaller:   skillInstaller,
		workspacePath:    workspacePath,
		subagentsCached:  newSubagentsCache(subagentsCacheTTL, subagentsCacheMaxKeys, time.Now),
	}, nil
}

func (n *NativeChannel) SetInboundSpooler(s InboundSpooler) {
	if n == nil || n.base == nil {
		return
	}
	n.base.SetInboundSpooler(s)
}

var ErrPeerNotReady = errors.New("native: no live client for peer")

func (n *NativeChannel) SetOutboundFlusher(f PeerFlusher) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.outboundFlusher = f
}

func (n *NativeChannel) hasOutboundFlusher() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.outboundFlusher != nil
}

func (n *NativeChannel) outboundFlusherSnapshot() PeerFlusher {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.outboundFlusher
}

func (n *NativeChannel) Name() string {
	return ChannelName
}

func (n *NativeChannel) getConfigPath() string {
	if n.configPath != "" {
		return n.configPath
	}
	return config.DefaultConfigPath()
}

func (n *NativeChannel) IsRunning() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.running
}

func (n *NativeChannel) IsAllowed(senderID string) bool {
	return true
}

func (n *NativeChannel) Start(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.running {
		return nil
	}

	n.startTime = time.Now()
	n.loopCtx, n.loopCancel = context.WithCancel(ctx)
	n.startLoop(n.loopCtx, n.runUploadCleanup)
	n.startLoop(n.loopCtx, n.runAuthRevocationReconcile)
	startCatalogPrefetch()

	n.running = true
	n.base.setRunning(true)

	return nil
}

func (n *NativeChannel) startLoop(ctx context.Context, run func(context.Context)) {
	n.activeLoops.Add(1)
	go func() {
		defer n.activeLoops.Add(-1)
		run(ctx)
	}()
}

const loopJoinTimeout = 2 * time.Second

func (n *NativeChannel) waitForLoops() {
	n.mu.Lock()
	cancel := n.loopCancel
	n.loopCancel = nil
	n.mu.Unlock()

	if cancel == nil {
		return
	}
	cancel()

	deadline := time.Now().Add(loopJoinTimeout)
	for n.activeLoops.Load() > 0 {
		if time.Now().After(deadline) {
			logger.WarnCF("native", "stop: background loops did not exit in time", map[string]interface{}{
				"active": n.activeLoops.Load(),
			})
			return
		}
		time.Sleep(time.Millisecond)
	}
}

var catalogPrefetchOnce sync.Once

func startCatalogPrefetch() {
	catalogPrefetchOnce.Do(func() {
		catalog.Ensure()
		go func() {
			_ = catalog.LoadDiskCache("")
			if !catalog.HasCachedIndex() || !catalog.IsPrefetchFresh(0) {
				ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
				defer cancel()
				_ = catalog.Refresh(ctx, catalog.Options{})
			}
		}()
	})
}

func (n *NativeChannel) Stop(ctx context.Context) error {
	defer n.waitForLoops()

	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.running {
		return nil
	}

	for id, client := range n.wsClients {
		client.mu.Lock()
		client.closed = true
		if client.reconnectTimer != nil {
			client.reconnectTimer.Stop()
			client.reconnectTimer = nil
		}
		client.reconnecting = false
		closeDoneChan(client.done)
		client.mu.Unlock()
		if client.Conn != nil {
			client.Conn.Close()
		}
		delete(n.wsClients, id)
	}
	for id, stream := range n.restStreams {
		close(stream.ch)
		delete(n.restStreams, id)
	}

	n.pinLimiter.Stop()
	n.pairLimiter.Stop()
	n.refreshLimiter.Stop()
	n.apiLimiter.Stop()
	n.wsMessageLimiter.Stop()
	n.authLogLimiter.Stop()

	n.running = false
	n.base.setRunning(false)

	return nil
}

func (n *NativeChannel) runUploadCleanup(ctx context.Context) {
	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	maxAge := time.Duration(n.cfg.UploadTTLHours) * time.Hour
	stagingDir := attachmentStagingDir(n.cfg.LeleDir)

	utils.CleanupOldUploads(uploadDir, maxAge)
	utils.CleanupOldUploads(stagingDir, maxAge)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			utils.CleanupOldUploads(uploadDir, maxAge)
			utils.CleanupOldUploads(stagingDir, maxAge)
		}
	}
}

func (n *NativeChannel) reconcileInterval() time.Duration {
	if n.authReconcileEvery > 0 {
		return n.authReconcileEvery
	}
	return authReconcileInterval
}

func (n *NativeChannel) runAuthRevocationReconcile(ctx context.Context) {
	if n.auth == nil {
		return
	}

	ticker := time.NewTicker(n.reconcileInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dropped, err := n.auth.reconcileClientsWithStore()
			if err != nil {
				logger.WarnCF("native", "auth: client reconciliation failed", map[string]interface{}{
					"error": err.Error(),
				})
				continue
			}
			if len(dropped) > 0 {
				n.dropWSClientsForAuth(dropped)
			}
		}
	}
}

func (n *NativeChannel) dropWSClientsForAuth(authClientIDs []string) {
	revoked := make(map[string]struct{}, len(authClientIDs))
	for _, id := range authClientIDs {
		if id != "" {
			revoked[id] = struct{}{}
		}
	}
	if len(revoked) == 0 {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	for _, client := range n.wsClients {
		if client.ClientInfo == nil {
			continue
		}
		if _, ok := revoked[client.ClientInfo.ClientID]; ok {
			logger.InfoCF("native", "dropping WebSocket of client revoked by another process", map[string]interface{}{
				"auth_client_id": client.ClientInfo.ClientID,
				"conn_id":        client.ID,
			})
			n.abandonWSClientLocked(client)
		}
	}
}

func (n *NativeChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	sessionKey := msg.ChatID
	if n.agentLoop != nil {
		sessionKey = n.agentLoop.ResolveSessionKey(sessionKey)
	}

	if msg.SpoolID != 0 && n.hasOutboundFlusher() && !n.hasLiveClientFor(sessionKey) {
		return ErrPeerNotReady
	}

	n.dispatchOutboundMessage(msg)
	return nil
}

func (n *NativeChannel) ConsumesEvent(string) bool { return true }

func (n *NativeChannel) RegisterRoutes(mux *http.ServeMux) {
	withAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return n.authMiddleware(h).ServeHTTP
	}

	withBodyLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			next.ServeHTTP(w, r)
		})
	}

	applyBodyLimit := func(h http.HandlerFunc) http.HandlerFunc {
		return withBodyLimit(h).ServeHTTP
	}

	mux.HandleFunc("GET /api/v1/auth/pin", n.rateLimitMiddleware(n.pinLimiter, withAuth(n.handleGetPIN)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/pair", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handlePair)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/refresh", n.rateLimitMiddleware(n.refreshLimiter, http.HandlerFunc(n.handleRefresh)).ServeHTTP)
	mux.HandleFunc("GET /api/v1/auth/status", n.rateLimitMiddleware(n.apiLimiter, http.HandlerFunc(n.handleAuthStatus)).ServeHTTP)
	mux.HandleFunc("GET /api/v1/auth/clients", withAuth(n.handleListClients))
	mux.HandleFunc("DELETE /api/v1/auth/clients/{clientID}", withAuth(n.handleRemoveClient))
	mux.HandleFunc("POST /api/v1/auth/logout", withAuth(n.handleLogout))

	mux.HandleFunc("GET /api/v1/ws", n.handleWebSocket)

	mux.HandleFunc("POST /api/v1/chat/send", withAuth(applyBodyLimit(n.handleChatSend)))
	mux.HandleFunc("POST /api/v1/chat/send/stream", withAuth(applyBodyLimit(n.handleChatSendStream)))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}", withAuth(n.handleStreamStatus))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}/{messageID}", withAuth(n.handleStreamState))
	mux.HandleFunc("GET /api/v1/chat/commands", withAuth(n.handleChatCommands))
	mux.HandleFunc("GET /api/v1/chat/sessions", withAuth(n.handleChatSessions))
	mux.HandleFunc("GET /api/v1/chat/sessions/meta", withAuth(n.handleChatSessionsMeta))
	mux.HandleFunc("POST /api/v1/chat/sessions", withAuth(applyBodyLimit(n.handleCreateSession)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionGet))
	mux.HandleFunc("DELETE /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionDelete))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/clear", withAuth(n.handleChatClear))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/approve", withAuth(applyBodyLimit(n.handleChatApprove)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/history", withAuth(n.handleChatHistory))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/history/{subagentId}", withAuth(n.handleChatHistory))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/model", withAuth(n.handleSessionModel))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/model", withAuth(applyBodyLimit(n.handleSessionModel)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/folder", withAuth(n.handleSessionFolder))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/folder", withAuth(applyBodyLimit(n.handleSessionFolder)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/agent", withAuth(n.handleSessionAgent))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/agent", withAuth(applyBodyLimit(n.handleSessionAgent)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/thinking", withAuth(n.handleSessionThinking))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/thinking", withAuth(applyBodyLimit(n.handleSessionThinking)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/name", withAuth(n.handleSessionName))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/name", withAuth(applyBodyLimit(n.handleSessionName)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/context", withAuth(n.handleSessionContext))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/summary", withAuth(n.handleSessionSummary))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/compact", withAuth(n.handleSessionCompact))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/subagents", withAuth(n.handleSessionSubagents))

	mux.HandleFunc("GET /api/v1/agents", withAuth(n.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{agentID}", withAuth(n.handleAgentInfo))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/status", withAuth(n.handleAgentStatus))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/catalog", withAuth(n.handleAgentCatalog))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/files", withAuth(n.handleAgentFiles))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/files/{fileName}", withAuth(n.handleAgentFileRead))
	mux.HandleFunc("PUT /api/v1/agents/{agentID}/files/{fileName}", withAuth(applyBodyLimit(n.handleAgentFileSave)))

	mux.HandleFunc("PUT /api/v1/agents/{agentID}/skills/{name}/toggle", withAuth(applyBodyLimit(n.handleAgentSkillToggle)))
	mux.HandleFunc("DELETE /api/v1/agents/{agentID}/skills/{name}", withAuth(n.handleAgentSkillRemove))
	mux.HandleFunc("POST /api/v1/agents/{agentID}/skills/install", withAuth(applyBodyLimit(n.handleAgentSkillInstall)))
	mux.HandleFunc("POST /api/v1/agents/{agentID}/skills/install-batch", withAuth(applyBodyLimit(n.handleAgentSkillInstallBatch)))

	mux.HandleFunc("GET /api/v1/agents/{agentID}/commands", withAuth(n.handleAgentCommands))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/commands/{name}", withAuth(n.handleAgentCommandDetail))
	mux.HandleFunc("POST /api/v1/agents/{agentID}/commands", withAuth(applyBodyLimit(n.handleAgentCommandCreate)))
	mux.HandleFunc("PUT /api/v1/agents/{agentID}/commands/{name}", withAuth(applyBodyLimit(n.handleAgentCommandUpdate)))
	mux.HandleFunc("DELETE /api/v1/agents/{agentID}/commands/{name}", withAuth(n.handleAgentCommandDelete))

	mux.HandleFunc("GET /api/v1/config", withAuth(n.handleGetConfig))
	mux.HandleFunc("PUT /api/v1/config", withAuth(applyBodyLimit(n.handlePutConfig)))
	mux.HandleFunc("POST /api/v1/config/validate", withAuth(applyBodyLimit(n.handleValidateConfig)))

	mux.HandleFunc("GET /api/v1/system/version", withAuth(n.handleSystemVersion))
	mux.HandleFunc("GET /api/v1/system/updates/check", n.rateLimitMiddleware(n.apiLimiter, withAuth(n.handleUpdatesCheck)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/system/updates/apply", withAuth(applyBodyLimit(n.handleUpdatesApply)))
	mux.HandleFunc("GET /api/v1/system/updates/status", withAuth(n.handleUpdatesStatus))
	mux.HandleFunc("POST /api/v1/system/updates/rollback", withAuth(n.handleUpdatesRollback))
	mux.HandleFunc("POST /api/v1/system/restart", withAuth(n.handleSystemRestart))

	mux.HandleFunc("GET /api/v1/tools", withAuth(n.handleTools))
	mux.HandleFunc("GET /api/v1/models", withAuth(n.handleModels))
	mux.HandleFunc("GET /api/v1/providers/{name}/models", withAuth(n.handleProviderModels))

	mux.HandleFunc("GET /api/v1/catalog/providers", withAuth(n.handleCatalogProviders))
	mux.HandleFunc("GET /api/v1/catalog/models", withAuth(n.handleCatalogModels))
	mux.HandleFunc("POST /api/v1/catalog/prefetch", withAuth(applyBodyLimit(n.handleCatalogPrefetch)))

	mux.HandleFunc("GET /api/v1/skills", withAuth(n.handleSkills))
	mux.HandleFunc("POST /api/v1/skills", withAuth(applyBodyLimit(n.handleSkillInstall)))
	mux.HandleFunc("GET /api/v1/skills/available", withAuth(n.handleSkillsAvailable))
	mux.HandleFunc("DELETE /api/v1/skills/{name}", withAuth(n.handleSkillRemove))
	mux.HandleFunc("POST /api/v1/skills/scan", withAuth(applyBodyLimit(n.handleSkillScan)))
	mux.HandleFunc("POST /api/v1/skills/install-batch", withAuth(applyBodyLimit(n.handleSkillInstallBatch)))
	mux.HandleFunc("PUT /api/v1/skills/{name}/toggle", withAuth(applyBodyLimit(n.handleSkillToggle)))
	mux.HandleFunc("GET /api/v1/skills/workspace-config", withAuth(n.handleSkillWorkspaceConfig))

	mux.HandleFunc("GET /api/v1/mcp", withAuth(n.handleMCPInventory))
	mux.HandleFunc("GET /api/v1/mcp/{layer}/{doc}", withAuth(n.handleMCPRawDoc))
	mux.HandleFunc("PUT /api/v1/mcp/{layer}/raw", withAuth(applyBodyLimit(n.handleMCPRawPut)))
	mux.HandleFunc("PUT /api/v1/mcp/{layer}/servers/{name}/toggle", withAuth(applyBodyLimit(n.handleMCPToggle)))
	mux.HandleFunc("POST /api/v1/mcp/validate", withAuth(applyBodyLimit(n.handleMCPValidate)))

	mux.HandleFunc("POST /api/v1/mcp/servers/{name}/auth", withAuth(applyBodyLimit(n.handleMCPAuthStart)))
	mux.HandleFunc("DELETE /api/v1/mcp/servers/{name}/auth", withAuth(n.handleMCPAuthLogout))
	mux.HandleFunc("GET /api/v1/mcp/auth/{flow_id}", withAuth(n.handleMCPAuthStatus))
	mux.HandleFunc("POST /api/v1/mcp/auth/{flow_id}/complete", withAuth(applyBodyLimit(n.handleMCPAuthComplete)))
	mux.HandleFunc("DELETE /api/v1/mcp/auth/{flow_id}", withAuth(n.handleMCPAuthCancel))
	mux.HandleFunc("GET /api/v1/status", withAuth(n.handleStatus))
	mux.HandleFunc("GET /api/v1/channels", withAuth(n.handleChannels))
	mux.HandleFunc("GET /api/v1/logs", withAuth(n.handleLogs))
	mux.HandleFunc("GET /api/v1/logs/dates", withAuth(n.handleLogsDates))

	mux.HandleFunc("GET /api/v1/background-exec", withAuth(n.handleBackgroundExecs))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/output", withAuth(n.handleBackgroundExecOutput))
	mux.HandleFunc("POST /api/v1/background-exec/{id}/stop", withAuth(n.handleBackgroundExecStop))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/stream", withAuth(n.handleBackgroundExecStream))

	mux.HandleFunc("GET /api/v1/cron", withAuth(n.handleCronList))
	mux.HandleFunc("POST /api/v1/cron", withAuth(applyBodyLimit(n.handleCronCreate)))
	mux.HandleFunc("GET /api/v1/cron/{id}", withAuth(n.handleCronGet))
	mux.HandleFunc("PUT /api/v1/cron/{id}", withAuth(applyBodyLimit(n.handleCronUpdate)))
	mux.HandleFunc("DELETE /api/v1/cron/{id}", withAuth(n.handleCronDelete))
	mux.HandleFunc("POST /api/v1/cron/{id}/enable", withAuth(n.handleCronEnable))
	mux.HandleFunc("POST /api/v1/cron/{id}/disable", withAuth(n.handleCronDisable))
	mux.HandleFunc("POST /api/v1/cron/{id}/run", withAuth(n.handleCronRun))

	mux.HandleFunc("GET /api/v1/secrets", withAuth(n.handleSecretsList))
	mux.HandleFunc("POST /api/v1/secrets", withAuth(applyBodyLimit(n.handleSecretCreate)))
	mux.HandleFunc("PUT /api/v1/secrets/{name}", withAuth(applyBodyLimit(n.handleSecretUpdate)))
	mux.HandleFunc("GET /api/v1/secrets/status", withAuth(n.handleSecretsStatus))
	mux.HandleFunc("GET /api/v1/secrets/audit", withAuth(n.handleSecretsAudit))
	mux.HandleFunc("GET /api/v1/secrets/{name}", withAuth(n.handleSecretGet))
	mux.HandleFunc("DELETE /api/v1/secrets/{name}", withAuth(n.handleSecretDelete))

	mux.HandleFunc("POST /api/v1/files/upload", withAuth(n.handleFileUpload))
	mux.HandleFunc("GET /api/v1/files/view", n.handleFileView)
	mux.HandleFunc("GET /api/v1/files/view-secure", withAuth(n.handleFileViewSecure))

	mux.HandleFunc("GET /api/v1/fs/list", withAuth(n.handleFsList))

	mux.HandleFunc("GET /api/v1/locales", withAuth(n.handleLocalesList))
	mux.HandleFunc("POST /api/v1/locales/refresh", withAuth(n.handleLocalesRefresh))
	mux.HandleFunc("GET /api/v1/locales/{code}", withAuth(n.handleLocaleGet))
	mux.HandleFunc("POST /api/v1/locales/{code}/install", n.rateLimitMiddleware(n.apiLimiter, withAuth(n.handleLocaleInstall)).ServeHTTP)
	mux.HandleFunc("DELETE /api/v1/locales/{code}", withAuth(n.handleLocaleUninstall))
}

func (n *NativeChannel) isOriginAllowed(origin string) bool {
	for _, allowedOrigin := range n.cfg.CORSOrigins {
		if origin == allowedOrigin {
			return true
		}
	}

	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}

	switch origin {
	case "tauri://localhost", "https://tauri.localhost":
		return true
	}

	if parsedOrigin.Scheme != "http" && parsedOrigin.Scheme != "https" {
		return false
	}
	originHost := parsedOrigin.Hostname()

	if originHost == "localhost" || originHost == "127.0.0.1" || originHost == "::1" {
		return true
	}

	serverHost := n.cfg.Host
	if serverHost == "" || serverHost == "0.0.0.0" || serverHost == "::" || serverHost == "*" {
		return false
	}
	return originHost == serverHost
}

func (n *NativeChannel) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	if parsedOrigin, err := url.Parse(origin); err == nil && parsedOrigin.Host == r.Host {
		return true
	}

	return n.isOriginAllowed(origin)
}

func (n *NativeChannel) authMiddleware(next http.Handler) http.Handler {
	reject := func(w http.ResponseWriter, r *http.Request, status int, message, code, reason string) {
		host := extractHost(r.RemoteAddr)
		if n.authLogLimiter.sample(host + "|auth") {
			log.Printf("WARNING: auth rejected %s %s from %s: %s (%s)", r.Method, r.URL.Path, host, reason, code)
		}
		writeError(w, status, message, code)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			reject(w, r, http.StatusUnauthorized, "missing authorization header", "auth_missing", "missing header")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			reject(w, r, http.StatusUnauthorized, "invalid authorization format", "auth_invalid_format", "not a Bearer token")
			return
		}

		client, valid := n.auth.ValidateToken(token)
		if !valid {
			reject(w, r, http.StatusUnauthorized, "invalid or expired token", "auth_invalid_token", "unknown or expired token")
			return
		}

		n.auth.UpdateLastSeen(client.ClientID)

		r.Header.Set("X-Client-Id", client.ClientID)
		r.Header.Set("X-Device-Name", client.DeviceName)

		next.ServeHTTP(w, r)
	})
}

func (n *NativeChannel) sendWSEvent(sessionKey, event string, data interface{}) {
	if sessionKey == "" {
		n.broadcastAll(event, data)
		return
	}
	n.broadcastToSession(sessionKey, event, data)
}

func (n *NativeChannel) dispatchOutboundMessage(msg bus.OutboundMessage) {
	sessionKey := msg.ChatID
	if n.agentLoop != nil {
		sessionKey = n.agentLoop.ResolveSessionKey(sessionKey)
	}

	if len(msg.Attachments) > 0 {
		staged := make([]bus.FileAttachment, len(msg.Attachments))
		copy(staged, msg.Attachments)
		stageAttachments(staged, n.cfg.LeleDir)
		msg.Attachments = staged
		if n.agentLoop != nil {
			n.agentLoop.AttachFilesToLastAssistant(sessionKey, attachmentsToProviders(staged))
		}
	}

	switch msg.Event {
	case "message.stream":
		done := msg.Metadata["done"] == "true"
		if n.agentLoop != nil {
			if !done {
				n.agentLoop.AppendAssistantChunk(sessionKey, msg.Content)
			} else {
				n.agentLoop.FinalizeAssistantMessage(sessionKey)
			}
		}
		n.emitNativeEvent(sessionKey, "message.stream",
			newWSStreamPayload(msg.MessageID, sessionKey, msg.Content, done, msg.Metadata), msg.MessageID)
		return
	case "message.thinking":
		if n.agentLoop != nil {
			n.agentLoop.AppendReasoningChunk(sessionKey, msg.Content)
		}
		n.emitNativeEvent(sessionKey, "message.thinking",
			newWSThinkingPayload(msg.MessageID, sessionKey, msg.Content, msg.Metadata), msg.MessageID)
		return
	case "message.discard":
		n.emitNativeEvent(sessionKey, "message.discard", WSMessageDiscardPayload{
			MessageID:  msg.MessageID,
			SessionKey: sessionKey,
			Reason:     msg.Metadata["reason"],
		}, msg.MessageID)
		return
	case "tool.executing":
		var toolArgs map[string]interface{}
		if argsStr := msg.Metadata["arguments"]; argsStr != "" {
			_ = json.Unmarshal([]byte(argsStr), &toolArgs)
		}
		n.emitNativeEvent(sessionKey, "tool.executing", WSToolExecutingPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Action:             msg.Metadata["action"],
			Arguments:          toolArgs,
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			ToolCallID:         msg.Metadata["tool_call_id"],
		}, "")
		return
	case "tool.result":
		result := msg.Content
		if msg.Metadata != nil && msg.Metadata["result"] != "" {
			result = msg.Metadata["result"]
		}
		if msg.Metadata["tool"] == "spawn" {
			n.invalidateSubagentsCache(sessionKey)
		}
		n.emitNativeEvent(sessionKey, "tool.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             result,
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			ToolCallID:         msg.Metadata["tool_call_id"],
		}, "")
		return
	case "subagent.result":
		n.invalidateSubagentsCache(sessionKey)
		n.emitNativeEvent(sessionKey, "subagent.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             msg.Metadata["result"],
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			ToolCallID:         msg.Metadata["tool_call_id"],
		}, "")
		return
	case "history.updated":
		sessionName := ""
		if n.agentLoop != nil {
			sessionName = n.agentLoop.GetName(sessionKey)
		}
		payload := map[string]interface{}{
			"session_key": sessionKey,
			"name":        sessionName,
		}
		if reason := msg.Metadata["reason"]; reason != "" {
			payload["reason"] = reason
		}
		n.emitNativeEvent(sessionKey, "history.updated", payload, "")
		return
	case "group.status":
		n.emitNativeEvent(sessionKey, "group.status", WSGroupStatusPayload{
			SessionKey:   sessionKey,
			GroupID:      msg.Metadata["group_id"],
			Status:       msg.Metadata["status"],
			Participants: msg.Metadata["participants"],
		}, "")
		return
	case "group.turn":
		layer, _ := strconv.Atoi(msg.Metadata["layer"])
		turnIndex, _ := strconv.Atoi(msg.Metadata["turn_index"])
		n.emitNativeEvent(sessionKey, "group.turn", WSGroupTurnPayload{
			SessionKey: sessionKey,
			GroupID:    msg.Metadata["group_id"],
			Speaker:    msg.Metadata["speaker"],
			Label:      msg.Metadata["label"],
			Role:       msg.Metadata["role"],
			Layer:      layer,
			TurnIndex:  turnIndex,
			Content:    msg.Content,
		}, "")
		return
	case "group.complete":
		layers, _ := strconv.Atoi(msg.Metadata["layers"])
		totalTokens, _ := strconv.Atoi(msg.Metadata["total_tokens"])
		n.emitNativeEvent(sessionKey, "group.complete", WSGroupCompletePayload{
			SessionKey:  sessionKey,
			GroupID:     msg.Metadata["group_id"],
			Strategy:    msg.Metadata["strategy"],
			Layers:      layers,
			TotalTokens: totalTokens,
			Content:     msg.Content,
		}, "")
		return
	case "group.tool":
		layer, _ := strconv.Atoi(msg.Metadata["layer"])
		turnIndex, _ := strconv.Atoi(msg.Metadata["turn_index"])
		n.emitNativeEvent(sessionKey, "group.tool", WSGroupToolPayload{
			SessionKey: sessionKey,
			GroupID:    msg.Metadata["group_id"],
			Speaker:    msg.Metadata["speaker"],
			Label:      msg.Metadata["label"],
			Layer:      layer,
			TurnIndex:  turnIndex,
			ToolCallID: msg.Metadata["tool_call_id"],
			Tool:       msg.Metadata["tool"],
			Status:     msg.Metadata["status"],
			Arguments:  msg.Metadata["arguments"],
			Result:     msg.Metadata["result"],
		}, "")
		return
	case "approval.request":
		n.emitNativeEvent(sessionKey, "approval.request", WSApprovalRequestPayload{
			ID:      msg.Metadata["id"],
			Command: msg.Metadata["command"],
			Reason:  msg.Metadata["reason"],
		}, "")
		return
	case "command.applied":
		n.emitNativeEvent(sessionKey, "command.applied", WSCommandAppliedPayload{
			SessionKey:  sessionKey,
			Command:     msg.Metadata["command"],
			Description: msg.Metadata["description"],
			Args:        msg.Metadata["args"],
			Agent:       msg.Metadata["agent"],
			Model:       msg.Metadata["model"],
			Source:      msg.Metadata["source"],
		}, "")
		return
	case "turn.end":
		return
	}

	messageID := msg.MessageID
	if messageID == "" {
		messageID = uuid.New().String()
	}

	isErrorMessage := msg.Metadata["error"] == "true"

	alreadyStreamed := n.agentLoop != nil && n.agentLoop.HasStreamedContent(sessionKey)

	if alreadyStreamed {
		n.emitNativeEvent(sessionKey, "message.complete", WSMessageCompletePayload{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			Content:     msg.Content,
			Attachments: attachmentsToMaps(msg.Attachments),
			Error:       isErrorMessage,
		}, messageID)

		n.emitNativeEvent(sessionKey, "history.updated", map[string]interface{}{
			"session_key": sessionKey,
			"name":        n.agentLoop.GetName(sessionKey),
		}, "")
		return
	}

	if msg.Content != "" {
		n.emitNativeEvent(sessionKey, "message.stream",
			newWSStreamPayload(messageID, sessionKey, msg.Content, true, msg.Metadata), messageID)
	}

	if msg.Content == "" && len(msg.Attachments) == 0 {
		var sessionName string
		if n.agentLoop != nil {
			sessionName = n.agentLoop.GetName(sessionKey)
		}
		n.emitNativeEvent(sessionKey, "message.complete", WSMessageCompletePayload{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			Content:     "",
			Attachments: nil,
			Error:       isErrorMessage,
		}, messageID)

		n.emitNativeEvent(sessionKey, "history.updated", map[string]interface{}{
			"session_key": sessionKey,
			"name":        sessionName,
		}, "")
		return
	}

	n.emitNativeEvent(sessionKey, "message.complete", WSMessageCompletePayload{
		MessageID:   messageID,
		SessionKey:  sessionKey,
		Content:     msg.Content,
		Attachments: attachmentsToMaps(msg.Attachments),
		Error:       isErrorMessage,
	}, messageID)

	n.emitNativeEvent(sessionKey, "history.updated", map[string]interface{}{
		"session_key": sessionKey,
		"name":        n.agentLoop.GetName(sessionKey),
	}, "")
}

const (
	wsReconnectWindow = 30 * time.Second
	wsMaxPendingMsgs  = 512
)

func (n *NativeChannel) addWSClient(client *WSClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.wsClients[client.ID] = client
}

func closeDoneChan(ch chan struct{}) {
	if ch == nil {
		return
	}
	defer func() { recover() }()
	close(ch)
}

func (n *NativeChannel) removeWSClient(clientID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if client, exists := n.wsClients[clientID]; exists {
		n.abandonWSClientLocked(client)
	}
}

func (n *NativeChannel) abandonWSClientLocked(client *WSClient) {
	client.mu.Lock()
	client.closed = true
	if client.reconnectTimer != nil {
		client.reconnectTimer.Stop()
		client.reconnectTimer = nil
	}
	client.reconnecting = false
	closeDoneChan(client.done)
	client.mu.Unlock()

	if client.Conn != nil {
		client.Conn.Close()
	}
	delete(n.wsClients, client.ID)
}

func (n *NativeChannel) removeAbandonedClients(ids []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, id := range ids {
		if client, exists := n.wsClients[id]; exists {
			n.abandonWSClientLocked(client)
		}
	}
}

func (n *NativeChannel) markWSClientReconnecting(client *WSClient) {
	client.mu.Lock()
	if !client.reconnecting {
		client.reconnecting = true
		client.disconnectedAt = time.Now()
		client.closed = false
		client.pendingMsgs = nil
		client.maxPendingMsgs = wsMaxPendingMsgs
	}
	closeDoneChan(client.done)
	client.done = make(chan struct{})

	oldConn := client.Conn
	if oldConn != nil && client.reconnecting {
		client.Conn = nil
	}
	client.mu.Unlock()
	if oldConn != nil {
		oldConn.Close()
	}

	client.mu.Lock()
	if client.reconnectTimer != nil {
		client.reconnectTimer.Stop()
	}
	client.reconnectTimer = time.AfterFunc(wsReconnectWindow, func() {
		n.removeWSClient(client.ID)
		logger.InfoCF("native", "WebSocket reconnect window expired", map[string]interface{}{
			"client_id": client.ID,
		})
	})
	client.mu.Unlock()

	logger.InfoCF("native", "WebSocket client entered reconnecting state", map[string]interface{}{
		"client_id":   client.ID,
		"session_key": client.SessionKey,
		"window_secs": int(wsReconnectWindow.Seconds()),
	})
}

func (n *NativeChannel) findReconnectingClient(userID string) *WSClient {
	n.mu.RLock()
	defer n.mu.RUnlock()
	for _, client := range n.wsClients {
		client.mu.Lock()
		isReconnecting := client.reconnecting
		matchesUser := client.ClientInfo != nil && client.ClientInfo.ClientID == userID
		client.mu.Unlock()
		if isReconnecting && matchesUser {
			return client
		}
	}
	return nil
}

func (n *NativeChannel) reconnectWSClient(client *WSClient, conn *websocket.Conn) []json.RawMessage {
	client.mu.Lock()
	if client.reconnectTimer != nil {
		client.reconnectTimer.Stop()
		client.reconnectTimer = nil
	}

	closeDoneChan(client.done)
	client.done = make(chan struct{})

	for {
		select {
		case <-client.SendChan:
		default:
			goto drained
		}
	}
drained:

	buffered := client.pendingMsgs

	client.Conn = conn
	client.reconnecting = false
	client.closed = false
	client.pendingMsgs = nil

	client.mu.Unlock()

	logger.InfoCF("native", "WebSocket client reconnected", map[string]interface{}{
		"client_id":       client.ID,
		"session_key":     client.SessionKey,
		"buffered_events": len(buffered),
	})

	return buffered
}

func (n *NativeChannel) hasLiveClientFor(sessionKey string) bool {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, client := range n.wsClients {
		matches := sessionKeyMatches(client.SessionKey, sessionKey)
		if !matches {
			client.mu.Lock()
			subs := make([]string, 0, len(client.Subscriptions))
			for k := range client.Subscriptions {
				subs = append(subs, k)
			}
			client.mu.Unlock()
			for _, subKey := range subs {
				if sessionKeyMatches(subKey, sessionKey) {
					matches = true
					break
				}
			}
		}
		if !matches && n.agentLoop != nil && client.SessionKey != "" {
			resolved := n.agentLoop.ResolveSessionKey(client.SessionKey)
			matches = sessionKeyMatches(resolved, sessionKey)
		}
		if !matches {
			continue
		}

		client.mu.Lock()
		live := !client.reconnecting && !client.closed
		client.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

func (n *NativeChannel) broadcastToSession(sessionKey string, event string, data interface{}) {
	msg := WSMessage{
		Version: WSProtocolVersion,
		Event:   event,
		Data:    mustMarshal(data),
	}
	payload := mustMarshal(msg)

	n.mu.RLock()
	var targets []*WSClient
	for _, client := range n.wsClients {
		if sessionKeyMatches(client.SessionKey, sessionKey) {
			targets = append(targets, client)
			continue
		}
		client.mu.Lock()
		subs := make([]string, 0, len(client.Subscriptions))
		for k := range client.Subscriptions {
			subs = append(subs, k)
		}
		client.mu.Unlock()
		for _, subKey := range subs {
			if sessionKeyMatches(subKey, sessionKey) {
				targets = append(targets, client)
				goto nextClient
			}
		}
		if n.agentLoop != nil && client.SessionKey != "" {
			resolved := n.agentLoop.ResolveSessionKey(client.SessionKey)
			if sessionKeyMatches(resolved, sessionKey) {
				targets = append(targets, client)
			}
		}
	nextClient:
	}
	n.mu.RUnlock()

	found := 0
	cleanup := make([]string, 0)
	for _, client := range targets {
		if err := client.QueueSend(payload); err != nil {
			cleanup = append(cleanup, client.ID)
		} else {
			found++
		}
	}

	if len(cleanup) > 0 {
		n.removeAbandonedClients(cleanup)
	}

	if found == 0 && event == "approval.request" {
		logger.WarnCF("native", "No clients matched for approval broadcast, falling back to all clients", map[string]interface{}{
			"session_key": sessionKey,
			"event":       event,
			"clients":     len(n.wsClients),
		})
		n.broadcastAll(event, data)
	}
}

func (n *NativeChannel) broadcastAll(event string, data interface{}) {
	msg := WSMessage{
		Version: WSProtocolVersion,
		Event:   event,
		Data:    mustMarshal(data),
	}
	payload := mustMarshal(msg)

	n.mu.RLock()
	targets := make([]*WSClient, 0, len(n.wsClients))
	for _, client := range n.wsClients {
		targets = append(targets, client)
	}
	n.mu.RUnlock()

	cleanup := make([]string, 0)
	for _, client := range targets {
		if err := client.QueueSend(payload); err != nil {
			cleanup = append(cleanup, client.ID)
		}
	}

	if len(cleanup) > 0 {
		n.removeAbandonedClients(cleanup)
	}
}

func (n *NativeChannel) processAttachments(paths []string, sessionKey string) []bus.FileAttachment {
	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")

	attachments := make([]bus.FileAttachment, 0, len(paths))
	for _, path := range paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}

		absPath, err = filepath.EvalSymlinks(absPath)
		if err != nil {
			logger.WarnCF("native", "Attachment path cannot be resolved",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
					"error":       err.Error(),
				})
			continue
		}

		if !isContainedIn(absPath, uploadDir) {
			logger.WarnCF("native", "Attachment path outside upload directory rejected",
				map[string]interface{}{
					"session_key":   sessionKey,
					"path":          path,
					"resolved_path": absPath,
				})
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil {
			logger.WarnCF("native", "Attachment file not accessible, skipping",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
				})
			continue
		}

		if info.Size() > n.cfg.MaxUploadSizeMB*1024*1024 {
			logger.WarnCF("native", "Attachment file too large, skipping",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
					"size":        info.Size(),
				})
			continue
		}

		mimeType := detectMimeType(absPath)

		attachments = append(attachments, bus.FileAttachment{
			Path:      absPath,
			Name:      filepath.Base(absPath),
			MIMEType:  mimeType,
			Kind:      "file",
			Temporary: isContainedIn(absPath, uploadDir),
		})
	}
	return attachments
}

func (n *NativeChannel) validateSessionOwnership(clientID, sessionKey string) bool {
	_, ok := n.auth.GetClient(clientID)
	if !ok {
		return false
	}

	isSubagent := strings.HasPrefix(sessionKey, "subagent:")
	if !isSubagent {
		if idx := strings.LastIndex(sessionKey, ":subagent-"); idx > 0 {
			isSubagent = true
		}
	}
	if isSubagent {
		if n.agentLoop == nil {
			return false
		}
		resolvedParent := n.agentLoop.GetSubagentParentSessionKey(sessionKey)
		if resolvedParent == "" {
			return false
		}
	}

	return true
}

func (c *WSClient) Send(data []byte) error {
	return c.QueueSend(data)
}

func (c *WSClient) QueueSend(data []byte) error {
	c.mu.Lock()
	done := c.done
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}
	if c.reconnecting {
		if len(c.pendingMsgs) >= c.maxPendingMsgs {
			c.mu.Unlock()
			return fmt.Errorf("reconnect buffer full, dropping message")
		}
		c.pendingMsgs = append(c.pendingMsgs, json.RawMessage(data))
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	timer := time.NewTimer(wsQueueSendTimeout)
	defer timer.Stop()

	select {
	case c.SendChan <- data:
		return nil
	case <-timer.C:
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			if c.Conn != nil {
				c.Conn.Close()
			}
		}
		c.mu.Unlock()
		return fmt.Errorf("send timeout, client disconnected")
	case <-done:
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			if c.Conn != nil {
				c.Conn.Close()
			}
		}
		c.mu.Unlock()
		return fmt.Errorf("client removed during send")
	}
}

func mustMarshal(v interface{}) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func attachmentsToMaps(attachments []bus.FileAttachment) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(attachments))
	for _, a := range attachments {
		result = append(result, map[string]interface{}{
			"name":      a.Name,
			"path":      a.Path,
			"mime_type": a.MIMEType,
			"kind":      a.Kind,
			"caption":   a.Caption,
		})
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string, code string) {
	writeJSON(w, status, APIError{
		Code:    code,
		Message: message,
		Error:   message,
	})
}

func getClientID(r *http.Request) string {
	return r.Header.Get("X-Client-Id")
}

func getQueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

func (n *NativeChannel) handleBackgroundExecs(w http.ResponseWriter, r *http.Request) {
	includeCompleted := r.URL.Query().Get("include_completed") == "true"
	processes := n.agentLoop.GetBackgroundExecs(includeCompleted)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"processes": processes,
	})
}

func (n *NativeChannel) handleBackgroundExecOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required", "missing_id")
		return
	}

	tail := 0
	if tailStr := r.URL.Query().Get("tail"); tailStr != "" {
		fmt.Sscanf(tailStr, "%d", &tail)
	}

	output, status, elapsedMs, err := n.agentLoop.GetBackgroundExecOutput(id, tail)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "exec_not_found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":         id,
		"output":     output,
		"status":     status,
		"elapsed_ms": elapsedMs,
	})
}

func (n *NativeChannel) handleBackgroundExecStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required", "missing_id")
		return
	}

	if err := n.agentLoop.StopBackgroundExec(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "exec_not_found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"stopped": true,
	})
}

func (n *NativeChannel) handleBackgroundExecStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required", "missing_id")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming not supported"}`, http.StatusInternalServerError)
		return
	}

	_, _, _, err := n.agentLoop.GetBackgroundExecOutput(id, 0)
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", mustMarshal(map[string]interface{}{"error": err.Error()}))
		flusher.Flush()
		return
	}

	lastLen := 0

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		logger.DebugCF("native", "bg exec stream: could not clear write deadline", map[string]interface{}{"error": err})
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	bgDeadline := time.After(24 * time.Hour)

	for {
		select {
		case <-r.Context().Done():
			return
		case <-bgDeadline:
			return
		case <-ticker.C:
			output, status, elapsedMs, err := n.agentLoop.GetBackgroundExecOutput(id, 0)
			if err != nil {
				_ = sseGuardedWrite(w, []byte("data: "+string(mustMarshal(map[string]interface{}{"error": err.Error()}))+"\n\n"))
				flusher.Flush()
				return
			}

			if len(output) > lastLen {
				newOutput := output[lastLen:]
				data := mustMarshal(map[string]interface{}{
					"output":     newOutput,
					"status":     status,
					"elapsed_ms": elapsedMs,
				})
				if err := sseGuardedWrite(w, []byte("data: "+string(data)+"\n\n")); err != nil {
					return
				}
				flusher.Flush()
				lastLen = len(output)
			}

			if status != "running" && lastLen > 0 {
				data := mustMarshal(map[string]interface{}{
					"output":     "",
					"status":     status,
					"elapsed_ms": elapsedMs,
					"done":       true,
				})
				if err := sseGuardedWrite(w, []byte("data: "+string(data)+"\n\n")); err != nil {
					return
				}
				flusher.Flush()
				return
			}
		}
	}
}

type cronScheduleInput struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type cronSpawnInput struct {
	Task     string `json:"task"`
	Label    string `json:"label,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Guidance string `json:"guidance,omitempty"`
	Model    string `json:"model,omitempty"`
}

type cronJobInput struct {
	Name     string            `json:"name"`
	Enabled  *bool             `json:"enabled,omitempty"`
	Schedule cronScheduleInput `json:"schedule"`
	Message  json.RawMessage   `json:"message,omitempty"`
	Command  json.RawMessage   `json:"command,omitempty"`
	Deliver  *bool             `json:"deliver,omitempty"`
	Channel  string            `json:"channel,omitempty"`
	To       string            `json:"to,omitempty"`
	Spawn    json.RawMessage   `json:"spawn,omitempty"`
}

func parseStringField(raw json.RawMessage) (string, bool, error) {
	if raw == nil {
		return "", false, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return "", true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", true, fmt.Errorf("invalid string field: %w", err)
	}
	return s, true, nil
}

func parseSpawnInput(raw json.RawMessage) (*cronSpawnInput, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "null" {
		return nil, true, nil
	}
	var in cronSpawnInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, true, fmt.Errorf("invalid spawn object: %w", err)
	}
	return &in, true, nil
}

func (n *NativeChannel) validateSpawnInput(in *cronSpawnInput) (*cron.SpawnConfig, error) {
	if in == nil {
		return nil, nil
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return nil, fmt.Errorf("spawn.task is required")
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID != "" {
		found := false
		for _, id := range n.agentLoop.ListAvailableAgentIDs() {
			if id == agentID {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown agent %q", agentID)
		}
	}
	model := strings.TrimSpace(in.Model)
	if model != "" {
		if err := n.validateSpawnModel(model); err != nil {
			return nil, err
		}
	}
	return &cron.SpawnConfig{
		Task:     task,
		Label:    strings.TrimSpace(in.Label),
		AgentID:  agentID,
		Guidance: strings.TrimSpace(in.Guidance),
		Model:    model,
	}, nil
}

func (n *NativeChannel) validateSpawnModel(model string) error {
	ref := providers.ParseModelRef(model, "")
	if ref == nil {
		return fmt.Errorf("invalid model %q", model)
	}
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}
	resolved := cfg.Providers.ResolveModelAlias(model, cfg.Agents.Defaults.Provider)
	providerName := cfg.Agents.Defaults.Provider
	if idx := strings.Index(resolved, ":"); idx > 0 {
		providerName = resolved[:idx]
	}
	if providerName == "" {
		return nil
	}
	if _, err := providers.CreateProviderForCandidate(cfg, providerName); err != nil {
		return fmt.Errorf("model %q cannot be used: %w", model, err)
	}
	return nil
}

func (n *NativeChannel) cronAvailable(w http.ResponseWriter) bool {
	if n.cronService == nil {
		writeError(w, http.StatusServiceUnavailable, "cron service not available", "cron_unavailable")
		return false
	}
	return true
}

func (n *NativeChannel) handleCronList(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	includeDisabled := r.URL.Query().Get("include_disabled") == "true"
	jobs := n.cronService.ListJobs(includeDisabled)
	if jobs == nil {
		jobs = []cron.CronJob{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":   jobs,
		"status": n.cronService.Status(),
	})
}

func (n *NativeChannel) handleCronGet(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	job := n.cronService.GetJob(id)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found", "cron_not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": job})
}

func (n *NativeChannel) handleCronCreate(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	var input cronJobInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_body")
		return
	}

	spawnIn, _, err := parseSpawnInput(input.Spawn)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_spawn")
		return
	}
	spawnConfig, err := n.validateSpawnInput(spawnIn)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_spawn")
		return
	}

	message, _, err := parseStringField(input.Message)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_body")
		return
	}
	command, _, err := parseStringField(input.Command)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_body")
		return
	}

	if message == "" && command == "" && spawnConfig == nil {
		writeError(w, http.StatusBadRequest, "message, command, or spawn task is required", "missing_message")
		return
	}

	schedule := cron.CronSchedule{
		Kind:    input.Schedule.Kind,
		AtMS:    input.Schedule.AtMS,
		EveryMS: input.Schedule.EveryMS,
		Expr:    input.Schedule.Expr,
		TZ:      input.Schedule.TZ,
	}
	if schedule.Kind == "" {
		writeError(w, http.StatusBadRequest, "schedule.kind is required (at|every|cron)", "missing_schedule")
		return
	}

	deliver := true
	if input.Deliver != nil {
		deliver = *input.Deliver
	}
	if command != "" || spawnConfig != nil {
		deliver = false
	}

	name := input.Name
	if name == "" {
		if spawnConfig != nil {
			name = spawnConfig.Task
		} else {
			name = message
		}
		if len(name) > 30 {
			name = name[:30]
		}
	}

	channel := input.Channel
	if channel == "" {
		channel = "native"
	}

	job, err := n.cronService.AddJob(name, schedule, message, deliver, channel, input.To)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "cron_add_failed")
		return
	}

	if command != "" {
		job.Payload.Command = command
	}
	if spawnConfig != nil {
		job.Payload.Spawn = spawnConfig
	}
	if command != "" || spawnConfig != nil {
		if err := n.cronService.UpdateJob(job); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "cron_add_failed")
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{"job": job})
}

func (n *NativeChannel) handleCronUpdate(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	existing := n.cronService.GetJob(id)
	if existing == nil {
		writeError(w, http.StatusNotFound, "job not found", "cron_not_found")
		return
	}

	var input cronJobInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_body")
		return
	}

	if input.Name != "" {
		existing.Name = input.Name
	}
	if input.Enabled != nil {
		existing.Enabled = *input.Enabled
	}
	if input.Schedule.Kind != "" {
		existing.Schedule = cron.CronSchedule{
			Kind:    input.Schedule.Kind,
			AtMS:    input.Schedule.AtMS,
			EveryMS: input.Schedule.EveryMS,
			Expr:    input.Schedule.Expr,
			TZ:      input.Schedule.TZ,
		}
	}
	if msg, msgProvided, err := parseStringField(input.Message); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_body")
		return
	} else if msgProvided {
		existing.Payload.Message = msg
	}
	if cmd, cmdProvided, err := parseStringField(input.Command); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_body")
		return
	} else if cmdProvided {
		existing.Payload.Command = cmd
	}
	if input.Deliver != nil {
		existing.Payload.Deliver = *input.Deliver
	}
	if input.Channel != "" {
		existing.Payload.Channel = input.Channel
	}
	if input.To != "" {
		existing.Payload.To = input.To
	}
	if spawnIn, spawnProvided, err := parseSpawnInput(input.Spawn); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_spawn")
		return
	} else if spawnProvided {
		spawnConfig, err := n.validateSpawnInput(spawnIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "invalid_spawn")
			return
		}
		existing.Payload.Spawn = spawnConfig
	}

	if existing.Payload.Message == "" && existing.Payload.Command == "" && existing.Payload.Spawn == nil {
		writeError(w, http.StatusBadRequest, "message, command, or spawn task is required", "missing_message")
		return
	}
	if existing.Payload.Command != "" || existing.Payload.Spawn != nil {
		existing.Payload.Deliver = false
	}

	if err := n.cronService.UpdateJob(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "cron_update_failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"job": existing})
}

func (n *NativeChannel) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if !n.cronService.RemoveJob(id) {
		writeError(w, http.StatusNotFound, "job not found", "cron_not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "removed": true})
}

func (n *NativeChannel) handleCronEnable(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	job := n.cronService.EnableJob(id, true)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found", "cron_not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": job})
}

func (n *NativeChannel) handleCronDisable(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	job := n.cronService.EnableJob(id, false)
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found", "cron_not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"job": job})
}

func (n *NativeChannel) handleCronRun(w http.ResponseWriter, r *http.Request) {
	if !n.cronAvailable(w) {
		return
	}
	id := r.PathValue("id")
	if err := n.cronService.RunJobNow(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error(), "cron_run_failed")
		return
	}
	job := n.cronService.GetJob(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"id": id, "ran": true, "job": job})
}
