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
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
)

const (
	// wsReadBufferSize is the size of the WebSocket read buffer in bytes.
	wsReadBufferSize = 8192
	// wsWriteBufferSize is the size of the WebSocket write buffer in bytes.
	wsWriteBufferSize = 8192
	// wsSendChanSize is the buffered channel capacity for outgoing messages.
	wsSendChanSize = 512
	// wsWriteTimeout is the deadline for normal write operations.
	wsWriteTimeout = 90 * time.Second
	// wsPingInterval is the interval between WebSocket ping frames.
	wsPingInterval = 30 * time.Second
	// wsReadDeadline is the read deadline reset on pong/ping and message receipt.
	wsReadDeadline = 90 * time.Second
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

	// Check if this user has a client in reconnecting state.
	existingClient := n.findReconnectingClient(clientInfo.ClientID)

	upgrader := websocket.Upgrader{
		ReadBufferSize:    wsReadBufferSize,
		WriteBufferSize:   wsWriteBufferSize,
		CheckOrigin:       n.checkOrigin,
		EnableCompression: true,
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.ErrorCF("native", "WebSocket upgrade failed", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	n.auth.TrackSessionKey(clientInfo.ClientID, sessionKey)
	n.auth.UpdateLastSeen(clientInfo.ClientID)

	if existingClient != nil {
		// Reconnect: resume the existing client with the new connection.
		buffered := n.reconnectWSClient(existingClient, conn)

		// If the session key changed, update it.
		if sessionKey != existingClient.SessionKey {
			existingClient.SessionKey = sessionKey
			if existingClient.Subscriptions == nil {
				existingClient.Subscriptions = make(map[string]bool)
			}
			existingClient.Subscriptions[sessionKey] = true
		}

		go n.wsReadLoop(existingClient)
		go n.wsWriteLoop(existingClient)

		// Send reconnected event with server state.
		n.sendReconnected(existingClient, buffered)
		return
	}

	// New connection: create a fresh WSClient.
	clientID := uuid.New().String()
	client := &WSClient{
		ID:            clientID,
		Conn:          conn,
		ClientInfo:    clientInfo,
		SessionKey:    sessionKey,
		Subscriptions: map[string]bool{sessionKey: true},
		SendChan:      make(chan []byte, wsSendChanSize),
	}

	n.addWSClient(client)

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
		n.markWSClientReconnecting(client)
		logger.InfoCF("native", "WebSocket client disconnected", map[string]interface{}{
			"client_id": client.ID,
		})
	}()

	conn := client.Conn
	conn.SetReadLimit(1024 * 1024)
	conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
	})
	conn.SetPingHandler(func(appData string) error {
		if err := conn.SetReadDeadline(time.Now().Add(wsReadDeadline)); err != nil {
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

		conn.SetReadDeadline(time.Now().Add(wsReadDeadline))

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
	pingTicker := time.NewTicker(wsPingInterval)
	defer pingTicker.Stop()

	for {
		// Blocking read from SendChan is intentional: the buffered channel
		// (wsSendChanSize) absorbs bursts while the goroutine sleeps when idle,
		// avoiding busy-wait. QueueSend handles overflow with a timeout.
		select {
		case data, ok := <-client.SendChan:
			if !ok {
				client.mu.Lock()
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				client.mu.Unlock()
				return
			}
			conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
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

	command := ""
	if n.approvalManager != nil {
		// HandleApproval atomically finds, removes and returns the approval
		handledApproval, err := n.approvalManager.HandleApproval(payload.RequestID, payload.Approved)
		if err != nil {
			logger.WarnCF("native", "Failed to handle approval", map[string]interface{}{
				"error":      err.Error(),
				"request_id": payload.RequestID,
			})
			n.sendError(client, "approval_error", "approval request expired or not found")
			return
		}
		command = handledApproval.Command

		// Persist the approval decision in session history
		n.persistApprovalMessage(client.SessionKey, payload.RequestID, payload.Approved, command, "")
	}

	// Broadcast approval result to the session
	n.emitNativeEvent(client.SessionKey, "approve.result", map[string]interface{}{
		"request_id": payload.RequestID,
		"approved":   payload.Approved,
		"command":    command,
	}, "")

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

	client.SessionKey = sessionKey

	if client.Subscriptions == nil {
		client.Subscriptions = make(map[string]bool)
	}
	client.Subscriptions[sessionKey] = true

	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(sessionKey)
	}

	ackData := map[string]interface{}{
		"session_key": sessionKey,
		"processing":  processing,
	}

	// Include in-progress messages so the frontend can restore streaming
	// content when the user switches back to a chat that is still processing.
	if processing {
		catchup := n.collectCatchupMessages(sessionKey, processing)
		if len(catchup) > 0 {
			ackData["in_progress_messages"] = catchup
		}
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

	sessionKey := payload.SessionKey
	if sessionKey == "" {
		sessionKey = client.SessionKey
	}

	// Broadcast typing indicator to clients subscribed to this session
	n.emitNativeEvent(sessionKey, "typing.indicator", map[string]interface{}{
		"session_key": sessionKey,
		"client_id":   client.ClientInfo.ClientID,
		"device_name": client.ClientInfo.DeviceName,
	}, "")
}

func (n *NativeChannel) handleWSCancel(client *WSClient, data json.RawMessage, eventID string) {
	logger.InfoCF("native", "Cancel request received", map[string]interface{}{
		"client_id":   client.ID,
		"session_key": client.SessionKey,
	})

	result := n.agentLoop.StopAgent(client.SessionKey)

	logger.InfoCF("native", "Cancel request processed", map[string]interface{}{
		"client_id":   client.ID,
		"session_key": client.SessionKey,
		"result":      result,
	})

	n.emitNativeEvent(client.SessionKey, "cancel.ack", map[string]interface{}{
		"status":      "cancelled",
		"session_key": client.SessionKey,
	}, "")
}

// collectCatchupMessages returns in-progress assistant messages for a session
// so the frontend can restore streaming content after a page reload or reconnect.
func (n *NativeChannel) collectCatchupMessages(sessionKey string, processing bool) []map[string]interface{} {
	if !processing || n.agentLoop == nil {
		return nil
	}
	inProgress := n.agentLoop.GetInProgressAssistant(sessionKey)
	if inProgress == nil {
		return nil
	}
	return []map[string]interface{}{{
		"role":              "assistant",
		"content":           inProgress.Content,
		"reasoning_content": inProgress.ReasoningContent,
	}}
}

// sessionGroupSnapshots returns GroupSnapshots whose OriginChatID matches
// the given sessionKey (after resolving session aliases).
func (n *NativeChannel) sessionGroupSnapshots(sessionKey string) []group.GroupSnapshot {
	all := n.agentLoop.AllGroupSnapshots()
	out := make([]group.GroupSnapshot, 0, len(all))
	for _, g := range all {
		if g.OriginChatID == sessionKey || n.agentLoop.ResolveSessionKey(g.OriginChatID) == sessionKey {
			out = append(out, g)
		}
	}
	return out
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

	catchupMessages := n.collectCatchupMessages(client.SessionKey, processing)

	groups := n.sessionGroupSnapshots(client.SessionKey)

	groupsEnabled := false
	if cfg := n.cfgSnapshot(); cfg != nil {
		groupsEnabled = cfg.Groups.Enabled
	}

	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "welcome",
		Data: mustMarshal(map[string]interface{}{
			"client_id":            client.ClientInfo.ClientID,
			"device_name":          client.ClientInfo.DeviceName,
			"session_key":          client.SessionKey,
			"status":               status,
			"agents":               agents,
			"server_time":          time.Now().Format(time.RFC3339),
			"processing":           processing,
			"in_progress_messages": catchupMessages,
			"groups":               groups,
			"groups_enabled":       groupsEnabled,
		}),
	})); err != nil {
		logger.WarnCF("native", "Failed to send welcome", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
	}
}

// sendReconnected sends a reconnected welcome event and flushes any buffered
// messages that accumulated while the client was disconnected.
func (n *NativeChannel) sendReconnected(client *WSClient, buffered []json.RawMessage) {
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

	disconnectedSecs := time.Since(client.disconnectedAt).Seconds()

	catchupMessages := n.collectCatchupMessages(client.SessionKey, processing)

	groups := n.sessionGroupSnapshots(client.SessionKey)

	groupsEnabled := false
	if cfg := n.cfgSnapshot(); cfg != nil {
		groupsEnabled = cfg.Groups.Enabled
	}

	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "reconnected",
		Data: mustMarshal(map[string]interface{}{
			"client_id":            client.ClientInfo.ClientID,
			"device_name":          client.ClientInfo.DeviceName,
			"session_key":          client.SessionKey,
			"status":               status,
			"agents":               agents,
			"server_time":          time.Now().Format(time.RFC3339),
			"processing":           processing,
			"buffered_events":      len(buffered),
			"disconnected_secs":    disconnectedSecs,
			"subscriptions":        client.Subscriptions,
			"in_progress_messages": catchupMessages,
			"groups":               groups,
			"groups_enabled":       groupsEnabled,
		}),
	})); err != nil {
		logger.WarnCF("native", "Failed to send reconnected event", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
		return
	}

	// Flush buffered events that accumulated during the disconnect window.
	// Skip message.stream and message.thinking events — their content is
	// already included in in_progress_messages (accumulated text) sent in
	// the reconnected payload. Replaying individual chunks would cause the
	// frontend to build a second assistant message from scratch, resulting
	// in duplicated/overlapping text.
	flushed := 0
	skipped := 0
	for _, payload := range buffered {
		// Peek at the event type without full deserialization.
		var peek struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(payload, &peek) == nil &&
			(peek.Event == "message.stream" || peek.Event == "message.thinking") {
			skipped++
			continue
		}
		if err := client.Send(payload); err != nil {
			logger.WarnCF("native", "Failed to flush buffered event during reconnect", map[string]interface{}{
				"client_id": client.ID,
				"error":     err.Error(),
			})
			return
		}
		flushed++
	}

	logger.InfoCF("native", "Reconnected client buffered events flushed", map[string]interface{}{
		"client_id":       client.ID,
		"buffered_events": len(buffered),
		"flushed":         flushed,
		"skipped_stream":  skipped,
	})
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
	n.emitNativeEvent(sessionKey, "message.stream", WSStreamPayload{
		MessageID:  messageID,
		SessionKey: sessionKey,
		Chunk:      chunk,
		Done:       done,
	}, messageID)
}

func (n *NativeChannel) SendToolExecuting(sessionKey, tool, action string) {
	n.emitNativeEvent(sessionKey, "tool.executing", WSToolExecutingPayload{
		Tool:   tool,
		Action: action,
	}, "")
}

func (n *NativeChannel) SendToolResult(sessionKey, tool, result string) {
	n.emitNativeEvent(sessionKey, "tool.result", WSToolResultPayload{
		Tool:   tool,
		Result: result,
	}, "")
}

func (n *NativeChannel) SendApprovalRequest(sessionKey, id, command, reason string) {
	n.emitNativeEvent(sessionKey, "approval.request", WSApprovalRequestPayload{
		ID:      id,
		Command: command,
		Reason:  reason,
	}, "")
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
