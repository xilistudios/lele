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

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	auth, err := NewAuthManager(&nativeCfg, leleDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	base := NewBaseChannel(ChannelName, nativeCfg, messageBus, []string{})

	pinLimiter := newRateLimiter(10, time.Minute)
	pairLimiter := newRateLimiter(5, time.Minute)
	apiLimiter := newRateLimiter(120, time.Minute)
	wsMessageLimiter := newRateLimiter(120, time.Minute)

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
	// Helper: wrap handler with auth middleware (which internally skips public paths)
	withAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return n.authMiddleware(h).ServeHTTP
	}

	// Public auth endpoints (auth middleware auto-skips /api/v1/auth/*, /api/v1/ws, /api/v1/files/view)
	mux.HandleFunc("/api/v1/auth/pin", n.rateLimitMiddleware(n.pinLimiter, http.HandlerFunc(n.handleGetPIN)).ServeHTTP)
	mux.HandleFunc("/api/v1/auth/pair", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handlePair)).ServeHTTP)
	mux.HandleFunc("/api/v1/auth/refresh", n.rateLimitMiddleware(n.pairLimiter, http.HandlerFunc(n.handleRefresh)).ServeHTTP)
	mux.HandleFunc("/api/v1/auth/status", n.rateLimitMiddleware(n.apiLimiter, http.HandlerFunc(n.handleAuthStatus)).ServeHTTP)
	mux.HandleFunc("/api/v1/ws", n.handleWebSocket)

	// Authenticated API endpoints
	mux.HandleFunc("/api/v1/chat/send", withAuth(n.handleChatSend))
	mux.HandleFunc("/api/v1/chat/history", withAuth(n.handleChatHistory))
	mux.HandleFunc("/api/v1/chat/sessions", withAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			n.handleCreateSession(w, r)
			return
		}
		n.handleChatSessions(w, r)
	}))
	mux.HandleFunc("/api/v1/chat/session/", withAuth(n.handleChatSession))
	mux.HandleFunc("/api/v1/agents", withAuth(n.handleAgents))
	mux.HandleFunc("/api/v1/agents/", withAuth(n.handleAgentInfo))
	mux.HandleFunc("/api/v1/config", withAuth(n.handleConfig))
	mux.HandleFunc("/api/v1/config/validate", withAuth(n.handleConfig))
	mux.HandleFunc("/api/v1/tools", withAuth(n.handleTools))
	mux.HandleFunc("/api/v1/models", withAuth(n.handleModels))
	mux.HandleFunc("/api/v1/providers/", withAuth(n.handleProviderModels))
	mux.HandleFunc("/api/v1/skills", withAuth(n.handleSkills))
	mux.HandleFunc("/api/v1/status", withAuth(n.handleStatus))
	mux.HandleFunc("/api/v1/channels", withAuth(n.handleChannels))
	mux.HandleFunc("/api/v1/files/upload", withAuth(n.handleFileUpload))
	mux.HandleFunc("/api/v1/files/view", n.handleFileView)
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
		path := r.URL.Path

		if path == "/api/v1/ws" || strings.HasPrefix(path, "/api/v1/auth/") || strings.HasPrefix(path, "/api/v1/files/view") {
			next.ServeHTTP(w, r)
			return
		}

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
		Event: event,
		Data:  mustMarshal(data),
	}
	payload := mustMarshal(msg)

	n.mu.RLock()
	var targets []*WSClient
	for _, client := range n.wsClients {
		// Match by current session key (active subscription)
		if client.SessionKey == sessionKey {
			targets = append(targets, client)
			continue
		}
		// Match by tracked subscriptions (sessions the client has subscribed to)
		if client.Subscriptions != nil && client.Subscriptions[sessionKey] {
			targets = append(targets, client)
			continue
		}
		// Match by resolved session key (subagent parent sessions)
		if n.agentLoop != nil && client.SessionKey != "" {
			resolved := n.agentLoop.ResolveSessionKey(client.SessionKey)
			if resolved == sessionKey {
				targets = append(targets, client)
			}
		}
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
}

func (n *NativeChannel) broadcastAll(event string, data interface{}) {
	msg := WSMessage{
		Event: event,
		Data:  mustMarshal(data),
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
	if strings.HasPrefix(sessionKey, "subagent:") {
		if n.agentLoop == nil {
			return false
		}
		resolvedParent := n.agentLoop.ResolveSessionKey(sessionKey)
		for _, sk := range client.SessionKeys {
			if sk == resolvedParent {
				return true
			}
		}
		return false
	}
	// Extract base session key (without timestamp suffix)
	baseSessionKey := sessionKey
	if idx := strings.LastIndex(sessionKey, ":"); idx > len("native:") {
		// Check if suffix is a timestamp (all digits)
		suffix := sessionKey[idx+1:]
		if len(suffix) > 0 {
			allDigits := true
			for _, c := range suffix {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				baseSessionKey = sessionKey[:idx]
			}
		}
	}
	for _, sk := range client.SessionKeys {
		// Exact match
		if sk == sessionKey {
			return true
		}
		// Allow base session key to match timestamped versions
		if sk == baseSessionKey {
			return true
		}
		// Allow timestamped session key to match base
		skBase := sk
		if idx := strings.LastIndex(sk, ":"); idx > len("native:") {
			suffix := sk[idx+1:]
			if len(suffix) > 0 {
				allDigits := true
				for _, c := range suffix {
					if c < '0' || c > '9' {
						allDigits = false
						break
					}
				}
				if allDigits {
					skBase = sk[:idx]
				}
			}
		}
		if skBase == baseSessionKey || skBase == sessionKey {
			return true
		}
	}
	return false
}

func (c *WSClient) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Conn != nil {
		return c.Conn.WriteMessage(websocket.TextMessage, data)
	}
	return fmt.Errorf("connection is nil")
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
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case c.SendChan <- data:
			return nil
		case <-timer.C:
			return fmt.Errorf("send channel full, client likely disconnected")
		}
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
	writeJSON(w, status, ErrorResponse{
		Error:   message,
		Message: message,
		Code:    code,
	})
}

func getClientID(r *http.Request) string {
	return r.Header.Get("X-Client-Id")
}

func getQueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}
