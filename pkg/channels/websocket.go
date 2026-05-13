package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
)

func (n *NativeChannel) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	token := getQueryParam(r, "token")
	if token == "" {
		hdr := r.Header.Get("Authorization")
		token = strings.TrimPrefix(hdr, "Bearer ")
	}

	if token == "" {
		writeError(w, http.StatusUnauthorized, "missing token", "token_missing")
		return
	}

	clientInfo, valid := n.auth.ValidateToken(token)
	if !valid {
		writeError(w, http.StatusUnauthorized, "invalid token", "token_invalid")
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     n.checkOrigin,
	}
	clientID := uuid.New().String()
	sessionKey := getQueryParam(r, "session_key")
	if sessionKey == "" {
		sessionKey = clientInfo.ClientID
	}

	// Validate session key before upgrade (response writer is still valid).
	if !isValidSessionKeyFormat(sessionKey) {
		writeError(w, http.StatusBadRequest, "invalid session_key format", "session_key_invalid")
		return
	}
	if !n.validateSessionOwnership(clientInfo.ClientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "forbidden")
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.ErrorCF("native", "WebSocket upgrade failed", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	client := &WSClient{
		ID:            clientID,
		Conn:          conn,
		ClientInfo:    clientInfo,
		SessionKey:    sessionKey,
		Subscriptions: map[string]bool{sessionKey: true},
		SendChan:      make(chan []byte, 256),
	}

	n.addWSClient(client)
	n.auth.TrackSessionKey(clientInfo.ClientID, sessionKey)
	n.auth.UpdateLastSeen(clientInfo.ClientID)

	logger.InfoCF("native", "WebSocket client connected", map[string]interface{}{
		"client_id":   clientID,
		"device_name": clientInfo.DeviceName,
		"session_key": sessionKey,
	})

	go n.wsReadLoop(client)
	go n.wsWriteLoop(client)

	n.sendWelcome(client)
}

func (n *NativeChannel) wsReadLoop(client *WSClient) {
	defer func() {
		n.removeWSClient(client.ID)
		logger.InfoCF("native", "WebSocket client disconnected", map[string]interface{}{
			"client_id": client.ID,
		})
	}()

	conn := client.Conn
	conn.SetReadLimit(1024 * 1024)
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})
	conn.SetPingHandler(func(appData string) error {
		if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.ErrorCF("native", "WebSocket read error", map[string]interface{}{
					"error": err.Error(),
				})
			}
			return
		}

		conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			n.sendError(client, "parse_error", "invalid message format")
			continue
		}

		n.handleWSMessage(client, msg)
	}
}

func (n *NativeChannel) wsWriteLoop(client *WSClient) {
	conn := client.Conn
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case data, ok := <-client.SendChan:
			if !ok {
				client.mu.Lock()
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				client.mu.Unlock()
				return
			}
			conn.SetWriteDeadline(time.Now().Add(90 * time.Second))
			client.mu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				client.mu.Unlock()
				logger.ErrorCF("native", "WebSocket write error", map[string]interface{}{
					"error": err.Error(),
				})
				return
			}
			client.mu.Unlock()
		case <-pingTicker.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			client.mu.Lock()
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				client.mu.Unlock()
				return
			}
			client.mu.Unlock()
		}
	}
}

func (n *NativeChannel) handleWSMessage(client *WSClient, msg WSMessage) {
	if msg.Version > 0 && msg.Version != WSProtocolVersion {
		n.sendError(client, "unsupported_version", fmt.Sprintf("unsupported protocol version: %d", msg.Version))
		return
	}

	eventID := msg.ID

	switch msg.Event {
	case "message":
		n.handleWSClientMessage(client, msg.Data, eventID)

	case "approve":
		n.handleWSApprove(client, msg.Data, eventID)

	case "subscribe":
		n.handleWSSubscribe(client, msg.Data, eventID)

	case "unsubscribe":
		n.handleWSUnsubscribe(client, msg.Data, eventID)

	case "typing":
		n.handleWSTyping(client, msg.Data)

	case "cancel":
		n.handleWSCancel(client, msg.Data, eventID)

	case "ping":
		if err := client.Send(mustMarshal(WSMessage{Version: WSProtocolVersion, Event: "pong", Data: mustMarshal(map[string]string{"time": time.Now().Format(time.RFC3339)})})); err != nil {
			logger.WarnCF("native", "Failed to send pong", map[string]interface{}{
				"client_id": client.ID,
				"error":     err.Error(),
			})
		}

	default:
		n.sendError(client, "unknown_event", "unknown event type: "+msg.Event)
	}
}

func (n *NativeChannel) handleWSClientMessage(client *WSClient, data json.RawMessage, eventID string) {
	if !n.wsMessageLimiter.allow(client.ClientInfo.ClientID) {
		n.sendError(client, "rate_limit_exceeded", "rate limit exceeded, please slow down")
		return
	}

	var payload WSMessagePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid message payload")
		return
	}

	if payload.Content == "" {
		n.sendError(client, "content_missing", "message content is required")
		return
	}

	sessionKey := payload.SessionKey
	if sessionKey == "" {
		sessionKey = client.SessionKey
	}

	if payload.SessionKey != "" && !isValidSessionKeyFormat(sessionKey) {
		n.sendError(client, "session_key_invalid", "invalid session_key format")
		return
	}

	if payload.SessionKey != "" && !n.validateSessionOwnership(client.ClientInfo.ClientID, sessionKey) {
		n.sendError(client, "forbidden", "access denied to this session")
		return
	}
	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	if payload.AgentID != "" {
		n.agentLoop.SetSessionAgent(sessionKey, payload.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(payload.Attachments, sessionKey)

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    client.ClientInfo.ClientID,
		ChatID:      sessionKey,
		Content:     payload.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	})

	ackData := map[string]string{"message_id": messageID, "session_key": sessionKey}
	ack := marshalWithID("message.ack", ackData, eventID)
	if err := client.Send(ack); err != nil {
		logger.WarnCF("native", "Failed to send message.ack", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
	}
}

func (n *NativeChannel) handleWSApprove(client *WSClient, data json.RawMessage, eventID string) {
	var payload WSApprovePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid approve payload")
		return
	}

	if n.approvalManager != nil {
		_, err := n.approvalManager.HandleApproval(payload.RequestID, payload.Approved)
		if err != nil {
			logger.WarnCF("native", "Failed to handle approval", map[string]interface{}{
				"error":      err.Error(),
				"request_id": payload.RequestID,
			})
			n.sendError(client, "approval_error", "approval request expired or not found")
			return
		}
	}

	ackData := map[string]string{"request_id": payload.RequestID, "approved": boolToString(payload.Approved)}
	if err := client.Send(marshalWithID("approve.ack", ackData, eventID)); err != nil {
		logger.WarnCF("native", "Failed to send approve.ack", map[string]interface{}{
			"client_id":  client.ID,
			"request_id": payload.RequestID,
			"error":      err.Error(),
		})
	}
}

func (n *NativeChannel) handleWSSubscribe(client *WSClient, data json.RawMessage, eventID string) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid subscribe payload")
		logger.WarnCF("native", "Subscribe payload parse error", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
		return
	}

	sessionKey := payload.SessionKey
	if !isValidSessionKeyFormat(sessionKey) {
		n.sendError(client, "session_key_invalid", "invalid session_key format")
		logger.WarnCF("native", "Subscribe invalid session_key format", map[string]interface{}{
			"client_id":   client.ID,
			"session_key": payload.SessionKey,
		})
		return
	}

	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	if !n.validateSessionOwnership(client.ClientInfo.ClientID, sessionKey) {
		n.sendError(client, "forbidden", "access denied to this session")
		logger.WarnCF("native", "Subscribe ownership validation failed", map[string]interface{}{
			"client_id":   client.ID,
			"session_key": payload.SessionKey,
		})
		return
	}

	oldSessionKey := client.SessionKey
	client.SessionKey = sessionKey

	if client.Subscriptions == nil {
		client.Subscriptions = make(map[string]bool)
	}
	client.Subscriptions[sessionKey] = true

	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	logger.InfoCF("native", "Client subscribed to session", map[string]interface{}{
		"client_id":       client.ID,
		"client_info_id":  client.ClientInfo.ClientID,
		"old_session_key": oldSessionKey,
		"new_session_key": sessionKey,
		"subscriptions":   len(client.Subscriptions),
	})

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(sessionKey)
	}

	ackData := map[string]interface{}{
		"session_key": sessionKey,
		"processing":  processing,
	}
	if err := client.Send(marshalWithID("subscribe.ack", ackData, eventID)); err != nil {
		logger.WarnCF("native", "Failed to send subscribe.ack", map[string]interface{}{
			"client_id":   client.ID,
			"session_key": sessionKey,
			"error":       err.Error(),
		})
	}
}

func (n *NativeChannel) handleWSUnsubscribe(client *WSClient, data json.RawMessage, eventID string) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid unsubscribe payload")
		logger.WarnCF("native", "Unsubscribe payload parse error", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
		return
	}

	oldSessionKey := client.SessionKey

	if payload.SessionKey != "" {
		delete(client.Subscriptions, payload.SessionKey)
	}

	if payload.SessionKey == "" || payload.SessionKey == client.SessionKey {
		client.SessionKey = client.ClientInfo.ClientID
	}

	logger.InfoCF("native", "Client unsubscribed from session", map[string]interface{}{
		"client_id":       client.ID,
		"old_session_key": oldSessionKey,
		"payload_key":     payload.SessionKey,
		"new_session_key": client.SessionKey,
		"subscriptions":   len(client.Subscriptions),
	})

	if err := client.Send(marshalWithID("unsubscribe.ack", map[string]string{"session_key": payload.SessionKey}, eventID)); err != nil {
		logger.WarnCF("native", "Failed to send unsubscribe.ack", map[string]interface{}{
			"client_id":   client.ID,
			"session_key": payload.SessionKey,
			"error":       err.Error(),
		})
	}
}

func (n *NativeChannel) handleWSTyping(client *WSClient, data json.RawMessage) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
}

func (n *NativeChannel) handleWSCancel(client *WSClient, data json.RawMessage, eventID string) {
	n.agentLoop.StopAgent(client.SessionKey)

	n.broadcastToSession(client.SessionKey, "cancel.ack", map[string]interface{}{
		"status":      "cancelled",
		"session_key": client.SessionKey,
	})
}

func (n *NativeChannel) sendWelcome(client *WSClient) {
	status := n.agentLoop.GetStatus(client.SessionKey)
	agents := make([]map[string]interface{}, 0)
	defaultID := n.agentLoop.GetDefaultAgentID()
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, map[string]interface{}{
				"id":        info.ID,
				"name":      info.Name,
				"workspace": info.Workspace,
				"model":     info.Model,
				"default":   info.ID == defaultID,
			})
		}
	}

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(client.SessionKey)
	}

	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "welcome",
		Data: mustMarshal(map[string]interface{}{
			"client_id":   client.ClientInfo.ClientID,
			"device_name": client.ClientInfo.DeviceName,
			"session_key": client.SessionKey,
			"status":      status,
			"agents":      agents,
			"server_time": time.Now().Format(time.RFC3339),
			"processing":  processing,
		}),
	})); err != nil {
		logger.WarnCF("native", "Failed to send welcome", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
	}
}

func (n *NativeChannel) sendError(client *WSClient, code, message string) {
	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "error",
		Data:    mustMarshal(WSErrorPayload{Code: code, Message: message}),
	})); err != nil {
		logger.WarnCF("native", "Failed to send error to client", map[string]interface{}{
			"client_id": client.ID,
			"code":      code,
			"error":     err.Error(),
		})
	}
}

func marshalWithID(event string, data interface{}, id string) json.RawMessage {
	return mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		ID:      id,
		Event:   event,
		Data:    mustMarshal(data),
	})
}

func (n *NativeChannel) StreamMessage(sessionKey, messageID, chunk string, done bool) {
	n.broadcastToSession(sessionKey, "message.stream", WSStreamPayload{
		MessageID:  messageID,
		SessionKey: sessionKey,
		Chunk:      chunk,
		Done:       done,
	})
}

func (n *NativeChannel) SendToolExecuting(sessionKey, tool, action string) {
	n.broadcastToSession(sessionKey, "tool.executing", WSToolExecutingPayload{
		Tool:   tool,
		Action: action,
	})
}

func (n *NativeChannel) SendToolResult(sessionKey, tool, result string) {
	n.broadcastToSession(sessionKey, "tool.result", WSToolResultPayload{
		Tool:   tool,
		Result: result,
	})
}

func (n *NativeChannel) SendApprovalRequest(sessionKey, id, command, reason string) {
	n.broadcastToSession(sessionKey, "approval.request", WSApprovalRequestPayload{
		ID:      id,
		Command: command,
		Reason:  reason,
	})
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// isValidSessionKeyFormat validates that a session key follows the expected format:
// - Non-empty, max 256 chars, printable ASCII only
// - No path traversal sequences (.., /, \)
// - native:{client_id} or native:{client_id}:{suffix} (deprecated, kept for backward compatibility)
// - subagent:{task_id} (deprecated, kept for backward compatibility)
// - Plain client IDs and custom session keys
// Returns true if the format is valid, false otherwise.
func isValidSessionKeyFormat(sessionKey string) bool {
	if strings.TrimSpace(sessionKey) == "" {
		return false
	}
	if len(sessionKey) > 256 {
		return false
	}
	// Reject path traversal and control characters
	if strings.Contains(sessionKey, "..") {
		return false
	}
	if strings.Contains(sessionKey, "/") || strings.Contains(sessionKey, "\\") {
		return false
	}
	for _, r := range sessionKey {
		if r < 32 || r > 126 {
			return false
		}
	}
	// Subagent keys have strict format requirements
	if strings.HasPrefix(sessionKey, "subagent:") {
		taskID := strings.TrimPrefix(sessionKey, "subagent:")
		if !strings.HasPrefix(taskID, "subagent-") {
			return false
		}
		if len(taskID) <= len("subagent-") {
			return false
		}
		for _, c := range taskID[len("subagent-"):] {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	// For backward compat, validate old native: format if present
	if strings.HasPrefix(sessionKey, "native:") {
		parts := strings.Split(sessionKey[7:], ":")
		if len(parts) < 1 || parts[0] == "" {
			return false
		}
		if len(parts) >= 2 && parts[1] == "" {
			return false
		}
		return true
	}
	// Accept any valid string for plain session keys
	return true
}

// sessionKeyMatches checks if two session keys match, handling backward compatibility
// for the deprecated native: prefix.
func sessionKeyMatches(a, b string) bool {
	if a == b {
		return true
	}
	// Strip native: prefix for comparison (backward compat)
	aNorm := strings.TrimPrefix(a, "native:")
	bNorm := strings.TrimPrefix(b, "native:")
	return aNorm == bNorm
}
