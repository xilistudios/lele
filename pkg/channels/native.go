package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/keyring"
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
	base             *BaseChannel
	cfg              *config.NativeConfig
	auth             *AuthManager
	bus              *bus.MessageBus
	agentLoop        AgentProvidable
	approvalManager  *ApprovalManager
	running          bool
	wsClients        map[string]*WSClient
	restStreams      map[string]*restStreamSubscriber
	leleDir          string
	configPath       string // path to config file, defaults to DefaultConfigPath() if empty
	mu               sync.RWMutex
	startTime        time.Time
	pinLimiter       *rateLimiter
	pairLimiter      *rateLimiter
	apiLimiter       *rateLimiter
	wsMessageLimiter *rateLimiter
	skillsLoader     *skills.SkillsLoader
	skillInstaller   *skills.SkillInstaller
	workspacePath    string
	reloadConfig     func() error // called after config save to reload runtime config
	cronService      CronProvidable
	keyringService   *keyring.Service
	updateService    *update.Updater
}

// CronProvidable is the interface for managing cron jobs via the API.
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

// SetCronService sets the cron service for API access.
func (n *NativeChannel) SetCronService(cs CronProvidable) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cronService = cs
}

// SetKeyringService sets the keyring service for API access.
func (n *NativeChannel) SetKeyringService(ks *keyring.Service) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.keyringService = ks
}

// SetReloadConfig sets a callback to be called after config is saved via the API.
// This enables the gateway to reload runtime config (agent registry, channels, etc.)
// synchronously instead of relying on the file watcher (which can be slow/unreliable).
func (n *NativeChannel) SetReloadConfig(fn func() error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reloadConfig = fn
}

// RegisterDesktopClient registers the built-in desktop client with the
// underlying auth manager. See AuthManager.RegisterDesktopClient.
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
	Subscriptions map[string]bool // all sessions this client is subscribed to
	SendChan      chan []byte
	closed        bool

	// Reconnection support
	reconnecting   bool
	disconnectedAt time.Time
	pendingMsgs    []json.RawMessage
	maxPendingMsgs int
	reconnectTimer *time.Timer
	mu             sync.Mutex
}

func NewNativeChannel(cfg *config.Config, messageBus *bus.MessageBus, agentLoop AgentProvidable, approvalManager *ApprovalManager) (*NativeChannel, error) {
	nativeCfg := cfg.Channels.Native

	leleDir := nativeCfg.LeleDir
	if leleDir == "" {
		leleDir = config.GetLeleDir()
	}

	auth, err := NewAuthManager(&nativeCfg, leleDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	base := NewBaseChannel(ChannelName, nativeCfg, messageBus, []string{})

	pinLimiter := newRateLimiter(10, time.Minute)
	pairLimiter := newRateLimiter(5, time.Minute)
	apiLimiter := newRateLimiter(120, time.Minute)
	wsMessageLimiter := newRateLimiter(120, time.Minute)

	workspacePath := cfg.WorkspacePath()
	globalSkillsDir := filepath.Join(leleDir, "skills")
	builtinSkillsDir := filepath.Join(leleDir, "lele", "skills")

	skillsLoader := skills.NewSkillsLoader(workspacePath, globalSkillsDir, builtinSkillsDir)
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
		apiLimiter:       apiLimiter,
		wsMessageLimiter: wsMessageLimiter,
		skillsLoader:     skillsLoader,
		skillInstaller:   skillInstaller,
		workspacePath:    workspacePath,
	}, nil
}

func (n *NativeChannel) Name() string {
	return ChannelName
}

// getConfigPath returns the path to the config file.
// If configPath is set, it uses that; otherwise it returns the default path.
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
	go n.runUploadCleanup(ctx)

	n.running = true
	n.base.setRunning(true)

	return nil
}

func (n *NativeChannel) Stop(ctx context.Context) error {
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
		client.mu.Unlock()
		close(client.SendChan)
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
	n.apiLimiter.Stop()
	n.wsMessageLimiter.Stop()

	n.running = false
	n.base.setRunning(false)

	return nil
}

func (n *NativeChannel) runUploadCleanup(ctx context.Context) {
	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	maxAge := time.Duration(n.cfg.UploadTTLHours) * time.Hour

	utils.CleanupOldUploads(uploadDir, maxAge)

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			utils.CleanupOldUploads(uploadDir, maxAge)
		}
	}
}

func (n *NativeChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	n.dispatchOutboundMessage(msg)
	return nil
}

// ConsumesEvent declares that the native channel interprets every protocol
// event itself (see dispatchOutboundMessage), including contentless ones such
// as message.stream with done=true and turn.end. It therefore opts out of the
// dispatcher's contentless-signal guard.
func (n *NativeChannel) ConsumesEvent(string) bool { return true }

// RegisterRoutes registers all native channel API routes on the given mux.
// This is called by the unified server to mount the native channel endpoints.
func (n *NativeChannel) RegisterRoutes(mux *http.ServeMux) {
	withAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return n.authMiddleware(h).ServeHTTP
	}

	withBodyLimit := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
			next.ServeHTTP(w, r)
		})
	}

	applyBodyLimit := func(h http.HandlerFunc) http.HandlerFunc {
		return withBodyLimit(h).ServeHTTP
	}

	// Public auth endpoints
	mux.HandleFunc("GET /api/v1/auth/pin", n.rateLimitMiddleware(n.pinLimiter, http.HandlerFunc(n.handleGetPIN)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/pair", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handlePair)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/refresh", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handleRefresh)).ServeHTTP)
	mux.HandleFunc("GET /api/v1/auth/status", n.rateLimitMiddleware(n.apiLimiter, http.HandlerFunc(n.handleAuthStatus)).ServeHTTP)
	mux.HandleFunc("GET /api/v1/auth/clients", withAuth(n.handleListClients))
	mux.HandleFunc("DELETE /api/v1/auth/clients/{clientID}", withAuth(n.handleRemoveClient))
	mux.HandleFunc("POST /api/v1/auth/logout", withAuth(n.handleLogout))

	// WebSocket
	mux.HandleFunc("GET /api/v1/ws", n.handleWebSocket)

	// Chat
	mux.HandleFunc("POST /api/v1/chat/send", withAuth(applyBodyLimit(n.handleChatSend)))
	mux.HandleFunc("POST /api/v1/chat/send/stream", withAuth(applyBodyLimit(n.handleChatSendStream)))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}", withAuth(n.handleStreamStatus))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}/{messageID}", withAuth(n.handleStreamState))
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

	// Agents
	mux.HandleFunc("GET /api/v1/agents", withAuth(n.handleAgents))
	mux.HandleFunc("GET /api/v1/agents/{agentID}", withAuth(n.handleAgentInfo))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/status", withAuth(n.handleAgentStatus))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/files", withAuth(n.handleAgentFiles))
	mux.HandleFunc("GET /api/v1/agents/{agentID}/files/{fileName}", withAuth(n.handleAgentFileRead))
	mux.HandleFunc("PUT /api/v1/agents/{agentID}/files/{fileName}", withAuth(applyBodyLimit(n.handleAgentFileSave)))

	// Config
	mux.HandleFunc("GET /api/v1/config", withAuth(n.handleGetConfig))
	mux.HandleFunc("PUT /api/v1/config", withAuth(applyBodyLimit(n.handlePutConfig)))
	mux.HandleFunc("POST /api/v1/config/validate", withAuth(applyBodyLimit(n.handleValidateConfig)))

	// System / self-update
	mux.HandleFunc("GET /api/v1/system/version", withAuth(n.handleSystemVersion))
	mux.HandleFunc("GET /api/v1/system/updates/check", n.rateLimitMiddleware(n.apiLimiter, withAuth(n.handleUpdatesCheck)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/system/updates/apply", withAuth(applyBodyLimit(n.handleUpdatesApply)))
	mux.HandleFunc("GET /api/v1/system/updates/status", withAuth(n.handleUpdatesStatus))
	mux.HandleFunc("POST /api/v1/system/updates/rollback", withAuth(n.handleUpdatesRollback))
	mux.HandleFunc("POST /api/v1/system/restart", withAuth(n.handleSystemRestart))

	// Tools, Models, Skills, Status, Channels
	mux.HandleFunc("GET /api/v1/tools", withAuth(n.handleTools))
	mux.HandleFunc("GET /api/v1/models", withAuth(n.handleModels))
	mux.HandleFunc("GET /api/v1/providers/{name}/models", withAuth(n.handleProviderModels))
	mux.HandleFunc("GET /api/v1/skills", withAuth(n.handleSkills))
	mux.HandleFunc("POST /api/v1/skills", withAuth(applyBodyLimit(n.handleSkillInstall)))
	mux.HandleFunc("GET /api/v1/skills/available", withAuth(n.handleSkillsAvailable))
	mux.HandleFunc("DELETE /api/v1/skills/{name}", withAuth(n.handleSkillRemove))
	mux.HandleFunc("POST /api/v1/skills/scan", withAuth(applyBodyLimit(n.handleSkillScan)))
	mux.HandleFunc("POST /api/v1/skills/install-batch", withAuth(applyBodyLimit(n.handleSkillInstallBatch)))
	mux.HandleFunc("PUT /api/v1/skills/{name}/toggle", withAuth(applyBodyLimit(n.handleSkillToggle)))
	mux.HandleFunc("GET /api/v1/skills/workspace-config", withAuth(n.handleSkillWorkspaceConfig))
	mux.HandleFunc("GET /api/v1/status", withAuth(n.handleStatus))
	mux.HandleFunc("GET /api/v1/channels", withAuth(n.handleChannels))
	mux.HandleFunc("GET /api/v1/logs", withAuth(n.handleLogs))
	mux.HandleFunc("GET /api/v1/logs/dates", withAuth(n.handleLogsDates))

	// Background execs
	mux.HandleFunc("GET /api/v1/background-exec", withAuth(n.handleBackgroundExecs))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/output", withAuth(n.handleBackgroundExecOutput))
	mux.HandleFunc("POST /api/v1/background-exec/{id}/stop", withAuth(n.handleBackgroundExecStop))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/stream", withAuth(n.handleBackgroundExecStream))

	// Cron jobs
	mux.HandleFunc("GET /api/v1/cron", withAuth(n.handleCronList))
	mux.HandleFunc("POST /api/v1/cron", withAuth(applyBodyLimit(n.handleCronCreate)))
	mux.HandleFunc("GET /api/v1/cron/{id}", withAuth(n.handleCronGet))
	mux.HandleFunc("PUT /api/v1/cron/{id}", withAuth(applyBodyLimit(n.handleCronUpdate)))
	mux.HandleFunc("DELETE /api/v1/cron/{id}", withAuth(n.handleCronDelete))
	mux.HandleFunc("POST /api/v1/cron/{id}/enable", withAuth(n.handleCronEnable))
	mux.HandleFunc("POST /api/v1/cron/{id}/disable", withAuth(n.handleCronDisable))
	mux.HandleFunc("POST /api/v1/cron/{id}/run", withAuth(n.handleCronRun))

	// Secrets (keyring)
	mux.HandleFunc("GET /api/v1/secrets", withAuth(n.handleSecretsList))
	mux.HandleFunc("POST /api/v1/secrets", withAuth(applyBodyLimit(n.handleSecretCreate)))
	mux.HandleFunc("GET /api/v1/secrets/status", withAuth(n.handleSecretsStatus))
	mux.HandleFunc("GET /api/v1/secrets/audit", withAuth(n.handleSecretsAudit))
	mux.HandleFunc("GET /api/v1/secrets/{name}", withAuth(n.handleSecretGet))
	mux.HandleFunc("DELETE /api/v1/secrets/{name}", withAuth(n.handleSecretDelete))

	// Files
	mux.HandleFunc("POST /api/v1/files/upload", withAuth(n.handleFileUpload))
	mux.HandleFunc("GET /api/v1/files/view", n.handleFileView)

	// Filesystem browsing (folder picker for the WebUI)
	mux.HandleFunc("GET /api/v1/fs/list", withAuth(n.handleFsList))
}

func (n *NativeChannel) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && n.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (n *NativeChannel) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self' ws: wss:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (n *NativeChannel) isOriginAllowed(origin string) bool {
	for _, allowedOrigin := range n.cfg.CORSOrigins {
		if origin == allowedOrigin {
			return true
		}
	}

	if parsedOrigin, err := url.Parse(origin); err == nil {
		originHost := parsedOrigin.Hostname()
		serverHost := n.cfg.Host
		if serverHost == "" {
			serverHost = "127.0.0.1"
		}

		if parsedOrigin.Scheme == "http" || parsedOrigin.Scheme == "https" {
			if originHost == serverHost || serverHost == "0.0.0.0" {
				return true
			}
		}
	}

	if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "tauri://") || strings.HasPrefix(origin, "https://tauri.localhost") {
		return true
	}

	return false
}

func (n *NativeChannel) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}

	// Allow same-origin connections: the Origin the page was loaded from
	// matches the host the request was sent to. This lets the WebUI open the
	// WebSocket via any IP/hostname it was served from (e.g. a LAN IP such as
	// http://192.168.0.171:18790) without explicit CORS configuration, while
	// still rejecting cross-origin requests. Mirrors gorilla/websocket's
	// default same-origin check.
	if parsedOrigin, err := url.Parse(origin); err == nil && parsedOrigin.Host == r.Host {
		return true
	}

	return n.isOriginAllowed(origin)
}

func (n *NativeChannel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing authorization header", "auth_missing")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == authHeader {
			writeError(w, http.StatusUnauthorized, "invalid authorization format", "auth_invalid_format")
			return
		}

		client, valid := n.auth.ValidateToken(token)
		if !valid {
			writeError(w, http.StatusUnauthorized, "invalid or expired token", "auth_invalid_token")
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
	switch msg.Event {
	case "message.stream":
		done := msg.Metadata["done"] == "true"
		// Persist stream chunk directly in the session file
		if n.agentLoop != nil {
			if !done {
				n.agentLoop.AppendAssistantChunk(sessionKey, msg.Content)
			} else {
				n.agentLoop.FinalizeAssistantMessage(sessionKey)
			}
		}
		n.emitNativeEvent(sessionKey, "message.stream", WSStreamPayload{
			MessageID:  msg.MessageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
			Done:       done,
		}, msg.MessageID)
		return
	case "message.thinking":
		if n.agentLoop != nil {
			n.agentLoop.AppendReasoningChunk(sessionKey, msg.Content)
		}
		n.emitNativeEvent(sessionKey, "message.thinking", WSThinkingPayload{
			MessageID:  msg.MessageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
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
		n.emitNativeEvent(sessionKey, "tool.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             result,
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			ToolCallID:         msg.Metadata["tool_call_id"],
		}, "")
		return
	case "subagent.result":
		n.emitNativeEvent(sessionKey, "subagent.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             msg.Metadata["result"],
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
			ToolCallID:         msg.Metadata["tool_call_id"],
		}, "")
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
	case "turn.end":
		// turn.end exists for channels that show transient "the bot is
		// working" state (Telegram's typing indicator). The WebUI derives its
		// processing state from message.ack/message.complete, so it has
		// nothing to clear here — and it must NOT fall through to the
		// full-message path below, which would emit a spurious empty
		// message.complete on every turn whose final message was suppressed.
		return
	}

	messageID := msg.MessageID
	if messageID == "" {
		messageID = uuid.New().String()
	}

	// Skip duplicate message.stream if content was already delivered via
	// streaming (the provider's ChatStream already sent chunks via onChunk).
	// This prevents the frontend from receiving two message.stream events
	// with Done=true, which causes visual flickering.
	//
	// HasStreamedContent checks if the session already has a streaming
	// assistant message with content. Since multi-iteration LLM runs stream
	// each response, the streaming message covers both the exact messageID
	// and iteration-suffixed variants.
	alreadyStreamed := n.agentLoop != nil && n.agentLoop.HasStreamedContent(sessionKey)

	if alreadyStreamed {
		// Content was already delivered via streaming chunks.
		// Only send message.complete + history.updated — these signal the
		// frontend to finalize the message and refetch canonical history.
		// Do NOT re-emit message.stream regardless of attachments.
		n.emitNativeEvent(sessionKey, "message.complete", WSMessageCompletePayload{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			Content:     msg.Content,
			Attachments: attachmentsToMaps(msg.Attachments),
		}, messageID)

		n.emitNativeEvent(sessionKey, "history.updated", map[string]interface{}{
			"session_key": sessionKey,
			"name":        n.agentLoop.GetName(sessionKey),
		}, "")
		return
	}

	if msg.Content != "" {
		n.emitNativeEvent(sessionKey, "message.stream", WSStreamPayload{
			MessageID:  messageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
			Done:       true,
		}, messageID)
	}

	if msg.Content == "" && len(msg.Attachments) == 0 {
		// Even with empty final content we MUST emit message.complete +
		// history.updated: message.complete is the turn-end signal the frontend
		// uses to clear its processing state and finalize the assistant
		// placeholder created on message.ack (streaming:true). Suppressing it
		// leaves the WebUI loading spinner stuck until the HTTP polling
		// safety-net kicks in (and the placeholder stale if the poll misses
		// it). An empty-content complete is safe client-side:
		// applyMessageComplete keeps the existing accumulated content when the
		// server sends none (web/src/hooks/streamingOps.ts).
		var sessionName string
		if n.agentLoop != nil {
			sessionName = n.agentLoop.GetName(sessionKey)
		}
		n.emitNativeEvent(sessionKey, "message.complete", WSMessageCompletePayload{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			Content:     "",
			Attachments: nil,
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
	}, messageID)

	// Signal that session data has been persisted and is safe to refetch
	n.emitNativeEvent(sessionKey, "history.updated", map[string]interface{}{
		"session_key": sessionKey,
		"name":        n.agentLoop.GetName(sessionKey),
	}, "")
}

const (
	// wsReconnectWindow is how long a disconnected client can reconnect
	// and resume its session without losing subscriptions or buffered events.
	wsReconnectWindow = 30 * time.Second
	// wsMaxPendingMsgs caps the number of events buffered during the reconnect window.
	wsMaxPendingMsgs = 512
)

func (n *NativeChannel) addWSClient(client *WSClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.wsClients[client.ID] = client
}

// removeWSClient permanently removes and cleans up a WebSocket client.
// It cancels any pending reconnect timer and frees all resources.
func (n *NativeChannel) removeWSClient(clientID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if client, exists := n.wsClients[clientID]; exists {
		client.mu.Lock()
		if client.reconnectTimer != nil {
			client.reconnectTimer.Stop()
			client.reconnectTimer = nil
		}
		client.reconnecting = false
		client.closed = true
		client.mu.Unlock()
		close(client.SendChan)
		if client.Conn != nil {
			client.Conn.Close()
		}
		delete(n.wsClients, clientID)
	}
}

// markWSClientReconnecting puts a client into reconnecting state instead of
// fully removing it. A 30-second timer is started; if the client doesn't
// reconnect before it fires, the client is permanently removed.
func (n *NativeChannel) markWSClientReconnecting(client *WSClient) {
	client.mu.Lock()
	client.reconnecting = true
	client.disconnectedAt = time.Now()
	client.closed = false // not closed yet, QueueSend will buffer
	client.pendingMsgs = nil
	client.maxPendingMsgs = wsMaxPendingMsgs
	// Close the old connection — the write loop goroutine is already dead.
	if client.Conn != nil {
		client.Conn.Close()
		client.Conn = nil
	}
	client.mu.Unlock()

	// Start the expiry timer. If the timer fires, remove permanently.
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

// findReconnectingClient looks for an existing client in reconnecting state
// that matches the given userID. Only one reconnecting slot per userID is
// allowed; the most recent disconnected session is returned.
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

// reconnectWSClient resumes a disconnected WebSocket client with a new
// connection. It restores the read/write loops, flushes buffered messages,
// and sends a reconnected welcome event.
func (n *NativeChannel) reconnectWSClient(client *WSClient, conn *websocket.Conn) []json.RawMessage {
	client.mu.Lock()
	// Stop the expiry timer
	if client.reconnectTimer != nil {
		client.reconnectTimer.Stop()
		client.reconnectTimer = nil
	}

	// Drain any stale messages from the old SendChan
	for {
		select {
		case <-client.SendChan:
		default:
			goto drained
		}
	}
drained:

	// Capture buffered messages before clearing state
	buffered := client.pendingMsgs

	// Restore active state
	client.Conn = conn
	client.reconnecting = false
	client.closed = false
	client.pendingMsgs = nil

	// Also add the new sessionKey to subscriptions if it changed
	client.mu.Unlock()

	logger.InfoCF("native", "WebSocket client reconnected", map[string]interface{}{
		"client_id":       client.ID,
		"session_key":     client.SessionKey,
		"buffered_events": len(buffered),
	})

	return buffered
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
		if client.Subscriptions != nil {
			for subKey := range client.Subscriptions {
				if sessionKeyMatches(subKey, sessionKey) {
					targets = append(targets, client)
					goto nextClient
				}
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
		n.mu.Lock()
		for _, id := range cleanup {
			if client, exists := n.wsClients[id]; exists {
				client.closed = true
				if client.Conn != nil {
					client.Conn.Close()
				}
				delete(n.wsClients, id)
			}
		}
		n.mu.Unlock()
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
		n.mu.Lock()
		for _, id := range cleanup {
			if client, exists := n.wsClients[id]; exists {
				client.closed = true
				if client.Conn != nil {
					client.Conn.Close()
				}
				delete(n.wsClients, id)
			}
		}
		n.mu.Unlock()
	}
}

func (n *NativeChannel) processAttachments(paths []string, sessionKey string) []bus.FileAttachment {
	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	absUploadDir, _ := filepath.Abs(uploadDir)

	attachments := make([]bus.FileAttachment, 0, len(paths))
	for _, path := range paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			continue
		}

		absPath, err = filepath.EvalSymlinks(absPath)
		if err != nil {
			continue
		}

		if absUploadDir != "" && !strings.HasPrefix(absPath, absUploadDir) {
			logger.WarnCF("native", "Attachment path outside upload directory rejected",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
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
			Temporary: strings.HasPrefix(absPath, absUploadDir),
		})
	}
	return attachments
}

func (n *NativeChannel) validateSessionOwnership(clientID, sessionKey string) bool {
	// Native channel clients are all on the same machine/lele instance.
	// All native sessions are shared across all native clients.
	// We still validate the client exists (valid auth token) but don't
	// restrict session access to individual clients.
	_, ok := n.auth.GetClient(clientID)
	if !ok {
		return false
	}

	// Subagent sessions require the parent session to exist in the agent loop
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

	// All native sessions are shared — any authenticated native client
	// can access any native session.
	return true
}

func (c *WSClient) Send(data []byte) error {
	return c.QueueSend(data)
}

func (c *WSClient) QueueSend(data []byte) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("client is closed")
	}
	// If reconnecting, buffer messages so they can be flushed on reconnection.
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

	timer := time.NewTimer(5 * time.Second)
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

// handleBackgroundExecs returns all background processes.
// GET /api/v1/background-exec?include_completed=true
func (n *NativeChannel) handleBackgroundExecs(w http.ResponseWriter, r *http.Request) {
	includeCompleted := r.URL.Query().Get("include_completed") == "true"
	processes := n.agentLoop.GetBackgroundExecs(includeCompleted)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"processes": processes,
	})
}

// handleBackgroundExecOutput returns the output of a specific background process.
// GET /api/v1/background-exec/{id}/output?tail=N
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

// handleBackgroundExecStop stops a running background process.
// POST /api/v1/background-exec/{id}/stop
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

// handleBackgroundExecStream provides real-time SSE streaming of background process output.
// GET /api/v1/background-exec/{id}/stream
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

	// Verify the process exists before starting the stream
	_, _, _, err := n.agentLoop.GetBackgroundExecOutput(id, 0)
	if err != nil {
		fmt.Fprintf(w, "data: %s\n\n", mustMarshal(map[string]interface{}{"error": err.Error()}))
		flusher.Flush()
		return
	}

	lastLen := 0
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			output, status, elapsedMs, err := n.agentLoop.GetBackgroundExecOutput(id, 0)
			if err != nil {
				fmt.Fprintf(w, "data: %s\n\n", mustMarshal(map[string]interface{}{"error": err.Error()}))
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
				fmt.Fprintf(w, "data: %s\n\n", data)
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
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
				return
			}
		}
	}
}

// ============================================================================
// Cron job handlers
// ============================================================================

// cronScheduleInput is the request body shape for creating/updating a schedule.
type cronScheduleInput struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

// cronSpawnInput mirrors cron.SpawnConfig for API input.
type cronSpawnInput struct {
	Task     string `json:"task"`
	Label    string `json:"label,omitempty"`
	AgentID  string `json:"agent_id,omitempty"`
	Guidance string `json:"guidance,omitempty"`
	Model    string `json:"model,omitempty"`
}

// cronJobInput is the request body for creating or updating a cron job.
// Message, Command and Spawn are raw JSON so updates can distinguish
// between "not provided" (nil), "explicitly cleared" (null), and "set".
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

// parseStringField decodes a raw JSON string field. It returns
// ("", false, nil) when omitted, ("", true, nil) for an explicit null,
// and the string value otherwise.
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

// parseSpawnInput decodes the raw spawn JSON. It returns (nil, false, nil)
// when the field was omitted, (nil, true, nil) for an explicit null (clear),
// and the parsed config otherwise.
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

// validateSpawnInput checks the spawn configuration and resolves it to the
// service type. The agent ID, when provided, must reference an existing agent.
// The model, when provided, must reference a configured provider.
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

// validateSpawnModel checks that a model override can actually be honored at
// runtime. It mirrors the resolver chain used by subagent spawns
// (ResolveModelAlias -> provider extraction -> CreateProviderForCandidate) so
// a model accepted at job-creation time cannot later be silently dropped.
// Models may be "provider:model" or a bare model name (resolved against the
// default provider at runtime).
func (n *NativeChannel) validateSpawnModel(model string) error {
	ref := providers.ParseModelRef(model, "")
	if ref == nil {
		return fmt.Errorf("invalid model %q", model)
	}
	cfg := n.cfgSnapshot()
	if cfg == nil {
		// Keep lenient behavior when no config snapshot is available.
		return nil
	}
	// Same alias resolution as the runtime resolver (pkg/agent tool_coordinator).
	resolved := cfg.Providers.ResolveModelAlias(model, cfg.Agents.Defaults.Provider)
	// Extract the provider name the same way the runtime does
	// (agent.extractProviderFromModel delegates to providers.ParseModelRef).
	// Inlined here to avoid importing pkg/agent from pkg/channels.
	providerName := cfg.Agents.Defaults.Provider
	if idx := strings.Index(resolved, ":"); idx > 0 {
		providerName = resolved[:idx]
	}
	if providerName == "" {
		// No explicit provider and no default: only a shape check is possible.
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

// handleCronList returns all cron jobs.
// GET /api/v1/cron?include_disabled=true
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

// handleCronGet returns a single cron job.
// GET /api/v1/cron/{id}
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

// handleCronCreate creates a new cron job.
// POST /api/v1/cron
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

	// Default channel to "native" so results are delivered to the WebUI.
	// When the channel is empty, ExecuteJob would default to "cli" which is
	// an internal channel whose outbound messages are silently dropped.
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

// handleCronUpdate updates an existing cron job.
// PUT /api/v1/cron/{id}
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
		// Explicitly provided: set ("spawn": {...}) or clear ("spawn": null).
		spawnConfig, err := n.validateSpawnInput(spawnIn)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "invalid_spawn")
			return
		}
		existing.Payload.Spawn = spawnConfig
	}

	// A job must retain at least one action.
	if existing.Payload.Message == "" && existing.Payload.Command == "" && existing.Payload.Spawn == nil {
		writeError(w, http.StatusBadRequest, "message, command, or spawn task is required", "missing_message")
		return
	}
	// Command and spawn jobs never deliver directly.
	if existing.Payload.Command != "" || existing.Payload.Spawn != nil {
		existing.Payload.Deliver = false
	}

	if err := n.cronService.UpdateJob(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "cron_update_failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"job": existing})
}

// handleCronDelete removes a cron job.
// DELETE /api/v1/cron/{id}
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

// handleCronEnable enables a cron job.
// POST /api/v1/cron/{id}/enable
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

// handleCronDisable disables a cron job.
// POST /api/v1/cron/{id}/disable
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

// handleCronRun triggers a cron job immediately.
// POST /api/v1/cron/{id}/run
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
