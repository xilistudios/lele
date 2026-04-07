package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type NativeChannel struct {
	base      *BaseChannel
	cfg       *config.NativeConfig
	auth      *AuthManager
	bus       *bus.MessageBus
	agentLoop AgentProvidable
	server    *http.Server
	running   bool
	wsClients map[string]*WSClient
	leleDir   string
	mu        sync.RWMutex
	startTime time.Time
}

type WSClient struct {
	ID         string
	Conn       *websocket.Conn
	ClientInfo *ClientInfo
	SessionKey string
	SendChan   chan []byte
	mu         sync.Mutex
}

func NewNativeChannel(cfg *config.Config, messageBus *bus.MessageBus, agentLoop AgentProvidable) (*NativeChannel, error) {
	nativeCfg := cfg.Channels.Native

	home, _ := os.UserHomeDir()
	leleDir := filepath.Join(home, ".lele")

	auth, err := NewAuthManager(&nativeCfg, leleDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth manager: %w", err)
	}

	base := NewBaseChannel(ChannelName, nativeCfg, messageBus, []string{})

	return &NativeChannel{
		base:      base,
		cfg:       &nativeCfg,
		auth:      auth,
		bus:       messageBus,
		agentLoop: agentLoop,
		wsClients: make(map[string]*WSClient),
		leleDir:   leleDir,
	}, nil
}

func (n *NativeChannel) Name() string {
	return ChannelName
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

	host := n.cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}

	port := n.cfg.Port
	if port <= 0 {
		port = 18793
	}

	addr := fmt.Sprintf("%s:%d", host, port)

	mux := http.NewServeMux()
	n.registerRoutes(mux)

	handler := n.corsMiddleware(n.authMiddleware(mux))

	n.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go n.listenForOutbound(ctx)
	go n.runUploadCleanup(ctx)

	go func() {
		logger.InfoCF("native", "Starting native channel server", map[string]interface{}{
			"address": addr,
		})
		n.startTime = time.Now()
		if err := n.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("native", "Server error", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}()

	n.running = true
	n.base.setRunning(true)

	logger.InfoC("native", "Native channel started successfully")
	return nil
}

func (n *NativeChannel) Stop(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if !n.running {
		return nil
	}

	for id, client := range n.wsClients {
		client.Conn.Close()
		delete(n.wsClients, id)
	}

	if n.server != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := n.server.Shutdown(shutdownCtx); err != nil {
			logger.ErrorCF("native", "Error shutting down server", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

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

func (n *NativeChannel) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/auth/pin", n.handleGetPIN)
	mux.HandleFunc("/api/v1/auth/pair", n.handlePair)
	mux.HandleFunc("/api/v1/auth/refresh", n.handleRefresh)
	mux.HandleFunc("/api/v1/auth/status", n.handleAuthStatus)
	mux.HandleFunc("/api/v1/ws", n.handleWebSocket)
	mux.HandleFunc("/api/v1/chat/send", n.handleChatSend)
	mux.HandleFunc("/api/v1/chat/history", n.handleChatHistory)
	mux.HandleFunc("/api/v1/chat/sessions", n.handleChatSessions)
	mux.HandleFunc("/api/v1/chat/session/", n.handleChatSession)
	mux.HandleFunc("/api/v1/agents", n.handleAgents)
	mux.HandleFunc("/api/v1/agents/", n.handleAgentInfo)
	mux.HandleFunc("/api/v1/config", n.handleConfig)
	mux.HandleFunc("/api/v1/tools", n.handleTools)
	mux.HandleFunc("/api/v1/models", n.handleModels)
	mux.HandleFunc("/api/v1/skills", n.handleSkills)
	mux.HandleFunc("/api/v1/status", n.handleStatus)
	mux.HandleFunc("/api/v1/channels", n.handleChannels)
	mux.HandleFunc("/api/v1/files/upload", n.handleFileUpload)
}

func (n *NativeChannel) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false

		for _, allowedOrigin := range n.cfg.CORSOrigins {
			if origin == allowedOrigin {
				allowed = true
				break
			}
		}

		if !allowed && (origin == "" || strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") || strings.HasPrefix(origin, "http://0.0.0.0") || strings.HasPrefix(origin, "tauri://") || strings.HasPrefix(origin, "https://tauri.localhost")) {
			allowed = true
		}

		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PATCH, OPTIONS")
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

func (n *NativeChannel) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if path == "/api/v1/ws" || strings.HasPrefix(path, "/api/v1/auth/") {
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

		r.Header.Set("X-Client-ID", client.ClientID)
		r.Header.Set("X-Device-Name", client.DeviceName)

		next.ServeHTTP(w, r)
	})
}

func (n *NativeChannel) listenForOutbound(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, ok := n.bus.SubscribeOutbound(ctx)
			if !ok {
				continue
			}

			if msg.Channel != ChannelName {
				continue
			}

			n.dispatchOutboundMessage(msg)
		}
	}
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
	case "tool.executing":
		n.sendWSEvent(sessionKey, "tool.executing", WSToolExecutingPayload{
			SessionKey: sessionKey,
			Tool:       msg.Metadata["tool"],
			Action:     msg.Metadata["action"],
		})
		return
	case "tool.result":
		result := msg.Content
		if msg.Metadata != nil && msg.Metadata["result"] != "" {
			result = msg.Metadata["result"]
		}
		n.sendWSEvent(sessionKey, "tool.result", WSToolResultPayload{
			SessionKey: sessionKey,
			Tool:       msg.Metadata["tool"],
			Result:     result,
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
		client.Conn.Close()
		delete(n.wsClients, clientID)
	}
}

func (n *NativeChannel) broadcastToSession(sessionKey string, event string, data interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()

	msg := WSMessage{
		Event: event,
		Data:  mustMarshal(data),
	}

	found := 0
	cleanup := make([]string, 0)
	for id, client := range n.wsClients {
		if client.SessionKey == sessionKey {
			if err := client.Send(mustMarshal(msg)); err != nil {
				// Client is disconnected, mark for cleanup
				cleanup = append(cleanup, id)
			} else {
				found++
			}
		} else if n.agentLoop != nil && client.SessionKey != "" {
			resolved := n.agentLoop.ResolveSessionKey(client.SessionKey)
			if resolved == sessionKey {
				if err := client.Send(mustMarshal(msg)); err != nil {
					// Client is disconnected, mark for cleanup
					cleanup = append(cleanup, id)
				} else {
					found++
				}
			}
		}
	}

	// Clean up disconnected clients
	for _, id := range cleanup {
		if client, exists := n.wsClients[id]; exists {
			client.Conn.Close()
			delete(n.wsClients, id)
		}
	}

	logger.InfoCF("native", "Broadcast to session", map[string]interface{}{
		"session_key": sessionKey,
		"event":       event,
		"clients":     len(n.wsClients),
		"matched":     found,
	})
}

func (n *NativeChannel) broadcastAll(event string, data interface{}) {
	n.mu.Lock()
	defer n.mu.Unlock()

	msg := WSMessage{
		Event: event,
		Data:  mustMarshal(data),
	}

	cleanup := make([]string, 0)
	for id, client := range n.wsClients {
		if err := client.Send(mustMarshal(msg)); err != nil {
			cleanup = append(cleanup, id)
		}
	}

	// Clean up disconnected clients
	for _, id := range cleanup {
		if client, exists := n.wsClients[id]; exists {
			client.Conn.Close()
			delete(n.wsClients, id)
		}
	}
}

func (c *WSClient) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Conn != nil {
		return c.Conn.WriteMessage(websocket.TextMessage, data)
	}
	return fmt.Errorf("connection is nil")
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
	return r.Header.Get("X-Client-ID")
}

func getQueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}
