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
}

type WSClient struct {
	ID            string
	Conn          *websocket.Conn
	ClientInfo    *ClientInfo
	SessionKey    string
	Subscriptions map[string]bool // all sessions this client is subscribed to
	SendChan      chan []byte
	closed        bool
	mu            sync.Mutex
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

	logger.InfoC("native", "Native channel started (routes registered via unified server)")
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
		client.mu.Unlock()
		close(client.SendChan)
		client.Conn.Close()
		delete(n.wsClients, id)
	}

	n.pinLimiter.Stop()
	n.pairLimiter.Stop()
	n.apiLimiter.Stop()
	n.wsMessageLimiter.Stop()

	n.running = false
	n.base.setRunning(false)

	logger.InfoC("native", "Native channel stopped")
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
		return withBodyLimit(http.HandlerFunc(h)).ServeHTTP
	}

	// Public auth endpoints
	mux.HandleFunc("GET /api/v1/auth/pin", n.rateLimitMiddleware(n.pinLimiter, http.HandlerFunc(n.handleGetPIN)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/pair", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handlePair)).ServeHTTP)
	mux.HandleFunc("POST /api/v1/auth/refresh", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handleRefresh)).ServeHTTP)
	mux.HandleFunc("GET /api/v1/auth/status", n.rateLimitMiddleware(n.apiLimiter, http.HandlerFunc(n.handleAuthStatus)).ServeHTTP)

	// WebSocket
	mux.HandleFunc("GET /api/v1/ws", n.handleWebSocket)

	// Chat
	mux.HandleFunc("POST /api/v1/chat/send", withAuth(applyBodyLimit(n.handleChatSend)))
	mux.HandleFunc("GET /api/v1/chat/sessions", withAuth(n.handleChatSessions))
	mux.HandleFunc("POST /api/v1/chat/sessions", withAuth(applyBodyLimit(n.handleCreateSession)))
	mux.HandleFunc("GET /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionGet))
	mux.HandleFunc("DELETE /api/v1/chat/sessions/{sessionKey}/{$}", withAuth(n.handleChatSessionDelete))
	mux.HandleFunc("POST /api/v1/chat/sessions/{sessionKey}/clear", withAuth(n.handleChatClear))
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
	logger.InfoCF("native", "Dispatching outbound message", map[string]interface{}{
		"session_key": sessionKey,
		"event":       msg.Event,
		"content_len": len(msg.Content),
		"message_id":  msg.MessageID,
	})
	switch msg.Event {
	case "message.stream":
		done := msg.Metadata["done"] == "true"
		n.sendWSEvent(sessionKey, "message.stream", WSStreamPayload{
			MessageID:  msg.MessageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
			Done:       done,
		})
		return
	case "message.thinking":
		n.sendWSEvent(sessionKey, "message.thinking", WSThinkingPayload{
			MessageID:  msg.MessageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
		})
		return
	case "tool.executing":
		var toolArgs map[string]interface{}
		if argsStr := msg.Metadata["arguments"]; argsStr != "" {
			_ = json.Unmarshal([]byte(argsStr), &toolArgs)
		}
		n.sendWSEvent(sessionKey, "tool.executing", WSToolExecutingPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Action:             msg.Metadata["action"],
			Arguments:          toolArgs,
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
		})
		return
	case "tool.result":
		result := msg.Content
		if msg.Metadata != nil && msg.Metadata["result"] != "" {
			result = msg.Metadata["result"]
		}
		n.sendWSEvent(sessionKey, "tool.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             result,
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
		})
		return
	case "subagent.result":
		n.sendWSEvent(sessionKey, "subagent.result", WSToolResultPayload{
			SessionKey:         sessionKey,
			Tool:               msg.Metadata["tool"],
			Result:             msg.Metadata["result"],
			SubagentSessionKey: msg.Metadata["subagent_session_key"],
		})
		return
	case "approval.request":
		n.sendWSEvent(sessionKey, "approval.request", WSApprovalRequestPayload{
			ID:      msg.Metadata["id"],
			Command: msg.Metadata["command"],
			Reason:  msg.Metadata["reason"],
		})
		return
	}

	messageID := msg.MessageID
	if messageID == "" {
		messageID = uuid.New().String()
	}

	if msg.Content != "" {
		n.sendWSEvent(sessionKey, "message.stream", WSStreamPayload{
			MessageID:  messageID,
			SessionKey: sessionKey,
			Chunk:      msg.Content,
			Done:       true,
		})
	}

	if msg.Content == "" && len(msg.Attachments) == 0 {
		return
	}

	n.sendWSEvent(sessionKey, "message.complete", WSMessageCompletePayload{
		MessageID:   messageID,
		SessionKey:  sessionKey,
		Content:     msg.Content,
		Attachments: attachmentsToMaps(msg.Attachments),
	})

	// Signal that session data has been persisted and is safe to refetch
	n.sendWSEvent(sessionKey, "history.updated", map[string]interface{}{
		"session_key": sessionKey,
		"name":        n.agentLoop.GetName(sessionKey),
	})
}

func (n *NativeChannel) addWSClient(client *WSClient) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.wsClients[client.ID] = client
}

func (n *NativeChannel) removeWSClient(clientID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if client, exists := n.wsClients[clientID]; exists {
		client.mu.Lock()
		client.closed = true
		client.mu.Unlock()
		close(client.SendChan)
		client.Conn.Close()
		delete(n.wsClients, clientID)
	}
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
		// Match by current session key (active subscription)
		if sessionKeyMatches(client.SessionKey, sessionKey) {
			targets = append(targets, client)
			continue
		}
		// Match by tracked subscriptions (sessions the client has subscribed to)
		if client.Subscriptions != nil {
			for subKey := range client.Subscriptions {
				if sessionKeyMatches(subKey, sessionKey) {
					targets = append(targets, client)
					goto nextClient
				}
			}
		}
		// Match by resolved session key (subagent parent sessions)
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
				client.Conn.Close()
				delete(n.wsClients, id)
			}
		}
		n.mu.Unlock()
	}

	logger.InfoCF("native", "Broadcast to session", map[string]interface{}{
		"session_key": sessionKey,
		"event":       event,
		"clients":     len(n.wsClients),
		"matched":     found,
	})

	// Fallback: if no clients matched and this is an approval request,
	// broadcast to all connected clients so the approval UI can receive it.
	if found == 0 && event == "approval.request" {
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
				client.Conn.Close()
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
	client, ok := n.auth.GetClient(clientID)
	if !ok {
		return false
	}
	// Check for subagent session key (old format: subagent:{id} or new format: {parent}:subagent-{n})
	isSubagent := strings.HasPrefix(sessionKey, "subagent:")
	if !isSubagent {
		// Check if it ends with subagent-{n} pattern (works with or without native: prefix)
		if idx := strings.LastIndex(sessionKey, ":subagent-"); idx > 0 {
			isSubagent = true
		}
	}
	if isSubagent {
		if n.agentLoop == nil {
			logger.WarnCF("native", "validateSessionOwnership: agentLoop is nil for subagent", map[string]interface{}{
				"session_key": sessionKey,
				"client_id":   clientID,
			})
			return false
		}
		resolvedParent := n.agentLoop.GetSubagentParentSessionKey(sessionKey)
		if resolvedParent == "" {
			logger.WarnCF("native", "validateSessionOwnership: resolved parent is empty", map[string]interface{}{
				"session_key": sessionKey,
				"client_id":   clientID,
			})
			return false
		}
		resolvedParent = strings.TrimPrefix(resolvedParent, "native:")

		if resolvedParent == clientID {
			return true
		}

		logger.InfoCF("native", "validateSessionOwnership: checking subagent parent", map[string]interface{}{
			"session_key":     sessionKey,
			"client_id":       clientID,
			"resolved_parent": resolvedParent,
			"client_keys":     client.SessionKeys,
		})
		resolvedParentBase := resolvedParent
		if strings.Contains(resolvedParent, ":chat:") {
			chatIdx := strings.LastIndex(resolvedParent, ":chat:")
			if chatIdx >= 0 {
				resolvedParentBase = resolvedParent[:chatIdx]
			}
		}
		for _, sk := range client.SessionKeys {
			skNorm := strings.TrimPrefix(sk, "native:")
			if skNorm == resolvedParent {
				return true
			}
			if skNorm == resolvedParentBase {
				return true
			}
			skBase := skNorm
			if strings.Contains(skNorm, ":chat:") {
				chatIdx := strings.LastIndex(skNorm, ":chat:")
				if chatIdx >= 0 {
					skBase = skNorm[:chatIdx]
				}
			}
			if skBase == resolvedParent || skBase == resolvedParentBase {
				return true
			}
		}
		logger.WarnCF("native", "validateSessionOwnership: no matching session key for subagent", map[string]interface{}{
			"session_key":          sessionKey,
			"client_id":            clientID,
			"resolved_parent":      resolvedParent,
			"resolved_parent_base": resolvedParentBase,
			"client_keys":          client.SessionKeys,
		})
		return false
	}
	// Default session key (client's own ID) is always valid — used for initial
	// WebSocket connections before any sessions have been created.
	// Also handles deprecated native: prefix for backward compatibility.
	if sessionKey == clientID || strings.TrimPrefix(sessionKey, "native:") == clientID {
		return true
	}
	for _, sk := range client.SessionKeys {
		// Exact match
		if sk == sessionKey {
			return true
		}
		// Backward compatibility: match with/without native: prefix
		if sessionKeyMatches(sk, sessionKey) {
			return true
		}
	}
	return false
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
	c.mu.Unlock()
	select {
	case c.SendChan <- data:
		return nil
	default:
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			c.Conn.Close()
		}
		c.mu.Unlock()
		return fmt.Errorf("send channel full, client disconnected")
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
	})
}

func getClientID(r *http.Request) string {
	return r.Header.Get("X-Client-Id")
}

func getQueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}
