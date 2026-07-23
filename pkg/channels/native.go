package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/skills"
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
}

// SetReloadConfig sets a callback to be called after config is saved via the API.
// This enables the gateway to reload runtime config (agent registry, channels, etc.)
// synchronously instead of relying on the file watcher (which can be slow/unreliable).
func (n *NativeChannel) SetReloadConfig(fn func() error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reloadConfig = fn
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

	// WebSocket
	mux.HandleFunc("GET /api/v1/ws", n.handleWebSocket)

	// Chat
	mux.HandleFunc("POST /api/v1/chat/send", withAuth(applyBodyLimit(n.handleChatSend)))
	mux.HandleFunc("POST /api/v1/chat/send/stream", withAuth(applyBodyLimit(n.handleChatSendStream)))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}", withAuth(n.handleStreamStatus))
	mux.HandleFunc("GET /api/v1/chat/streams/{sessionKey}/{messageID}", withAuth(n.handleStreamState))
	mux.HandleFunc("GET /api/v1/chat/sessions", withAuth(n.handleChatSessions))
	mux.HandleFunc("POST /api/v1/chat/sessions", withAuth(applyBodyLimit(n.handleCreateSession)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionGet))
	mux.HandleFunc("DELETE /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionDelete))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/clear", withAuth(n.handleChatClear))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/approve", withAuth(applyBodyLimit(n.handleChatApprove)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/history", withAuth(n.handleChatHistory))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/history/{subagentId}", withAuth(n.handleChatHistory))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/model", withAuth(n.handleSessionModel))
	mux.HandleFunc("PATCH /api/v1/chat/sessions/{sessionKey}/model", withAuth(applyBodyLimit(n.handleSessionModel)))
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

	// Tools, Models, Skills, Status, Channels
	mux.HandleFunc("GET /api/v1/tools", withAuth(n.handleTools))
	mux.HandleFunc("GET /api/v1/models", withAuth(n.handleModels))
	mux.HandleFunc("GET /api/v1/providers/{name}/models", withAuth(n.handleProviderModels))
	mux.HandleFunc("GET /api/v1/skills", withAuth(n.handleSkills))
	mux.HandleFunc("POST /api/v1/skills", withAuth(applyBodyLimit(n.handleSkillInstall)))
	mux.HandleFunc("GET /api/v1/skills/available", withAuth(n.handleSkillsAvailable))
	mux.HandleFunc("DELETE /api/v1/skills/{name}", withAuth(n.handleSkillRemove))
	mux.HandleFunc("GET /api/v1/status", withAuth(n.handleStatus))
	mux.HandleFunc("GET /api/v1/channels", withAuth(n.handleChannels))

	// Background execs
	mux.HandleFunc("GET /api/v1/background-exec", withAuth(n.handleBackgroundExecs))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/output", withAuth(n.handleBackgroundExecOutput))
	mux.HandleFunc("POST /api/v1/background-exec/{id}/stop", withAuth(n.handleBackgroundExecStop))
	mux.HandleFunc("GET /api/v1/background-exec/{id}/stream", withAuth(n.handleBackgroundExecStream))

	// Files
	mux.HandleFunc("POST /api/v1/files/upload", withAuth(n.handleFileUpload))
	mux.HandleFunc("GET /api/v1/files/view", n.handleFileView)
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
	case "approval.request":
		n.emitNativeEvent(sessionKey, "approval.request", WSApprovalRequestPayload{
			ID:      msg.Metadata["id"],
			Command: msg.Metadata["command"],
			Reason:  msg.Metadata["reason"],
		}, "")
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
