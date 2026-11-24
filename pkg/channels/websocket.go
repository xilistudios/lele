package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/logger"
)

const (
	wsReadBufferSize  = 8192
	wsWriteBufferSize = 8192
	wsSendChanSize    = 512
	wsWriteTimeout    = 90 * time.Second
	wsPingInterval    = 30 * time.Second
	wsReadDeadline    = 90 * time.Second
)

var wsQueueSendTimeout = 5 * time.Second

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

	if !isValidSessionKeyFormat(sessionKey) {
		writeError(w, http.StatusBadRequest, "invalid session_key format", "session_key_invalid")
		return
	}
	if !n.validateSessionOwnership(clientInfo.ClientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "forbidden")
		return
	}

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
		buffered := n.reconnectWSClient(existingClient, conn)

		if sessionKey != existingClient.SessionKey {
			existingClient.mu.Lock()
			existingClient.SessionKey = sessionKey
			if existingClient.Subscriptions == nil {
				existingClient.Subscriptions = make(map[string]bool)
			}
			existingClient.Subscriptions[sessionKey] = true
			existingClient.mu.Unlock()
		}

		go n.wsReadLoop(existingClient)
		go n.wsWriteLoop(existingClient)

		n.sendReconnected(existingClient, buffered)
		return
	}

	clientID := uuid.New().String()
	client := newWSClient(clientID, conn, clientInfo, sessionKey)

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

func newWSClient(id string, conn *websocket.Conn, info *ClientInfo, sessionKey string) *WSClient {
	c := &WSClient{
		ID:            id,
		Conn:          conn,
		ClientInfo:    info,
		SessionKey:    sessionKey,
		Subscriptions: map[string]bool{sessionKey: true},
		SendChan:      make(chan []byte, wsSendChanSize),
		done:          make(chan struct{}),
	}
	c.doneClosed = c.isDoneClosed
	return c
}

func (c *WSClient) isDoneClosed() bool {
	done := c.snapshotDone()
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func (c *WSClient) snapshotDone() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.done
}

func (n *NativeChannel) wsReadLoop(client *WSClient) {
	defer func() {
		n.markWSClientReconnecting(client)
		logger.InfoCF("native", "WebSocket client disconnected", map[string]interface{}{
			"client_id": client.ID,
		})
	}()

	done := client.snapshotDone()

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

	doneFired := make(chan struct{})
	closeDoneFired := sync.OnceFunc(func() { close(doneFired) })
	go func() {
		select {
		case <-done:
			conn.SetReadDeadline(time.Now())
			closeDoneFired()
		case <-doneFired:
		}
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-doneFired:
			default:
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					logger.ErrorCF("native", "WebSocket read error", map[string]interface{}{
						"error": err.Error(),
					})
				}
			}
			closeDoneFired()
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
	client.activeWriteLoops.Add(1)
	defer client.activeWriteLoops.Add(-1)

	done := client.snapshotDone()

	pingTicker := time.NewTicker(wsPingInterval)
	defer pingTicker.Stop()

	for {
		select {
		case <-done:
			return
		case data, ok := <-client.SendChan:
			if !ok {
				client.mu.Lock()
				if conn := client.Conn; conn != nil {
					conn.WriteMessage(websocket.CloseMessage, []byte{})
				}
				client.mu.Unlock()
				return
			}
			client.mu.Lock()
			conn := client.Conn
			if conn == nil {
				client.mu.Unlock()
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				client.mu.Unlock()
				logger.ErrorCF("native", "WebSocket write error", map[string]interface{}{
					"error": err.Error(),
				})
				return
			}
			client.mu.Unlock()
		case <-pingTicker.C:
			client.mu.Lock()
			conn := client.Conn
			if conn == nil {
				client.mu.Unlock()
				continue
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
	if n.wsMessageLimiter != nil && !n.wsMessageLimiter.allow(client.ClientInfo.ClientID) {
		n.sendError(client, "rate_limit_exceeded", "rate limit exceeded, please slow down")
		return
	}

	var payload WSMessagePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid message payload")
		return
	}

	if !hasSendablePayload(payload.Content, payload.Attachments) {
		n.sendError(client, "content_missing", "message content or an attachment is required")
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

	if !n.validateSessionOwnership(client.ClientInfo.ClientID, sessionKey) {
		n.sendError(client, "forbidden", "access denied to this session")
		return
	}

	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	if payload.AgentID != "" {
		n.agentLoop.HintSessionAgent(sessionKey, payload.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(payload.Attachments, sessionKey)

	if !hasSendablePayload(payload.Content, attachments) {
		n.sendError(client, "attachments_rejected", "attachments could not be read; add text or re-upload")
		return
	}

	msg := bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    client.ClientInfo.ClientID,
		ChatID:      sessionKey,
		Content:     payload.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	}
	n.base.publishInbound(&msg)

	ackSessionKey := sessionKey
	if n.agentLoop != nil {
		ackSessionKey = n.agentLoop.ResolveSessionKey(sessionKey)
	}
	ackData := map[string]string{"message_id": messageID, "session_key": ackSessionKey}
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
		handledApproval, err := n.approvalManager.HandleApprovalForSession(payload.RequestID, payload.Approved, client.SessionKey)
		if err != nil {
			if strings.Contains(err.Error(), "approval session mismatch") {
				logger.WarnCF("native", "Approval session mismatch rejected", map[string]interface{}{
					"client_id":  client.ID,
					"session":    client.SessionKey,
					"request_id": payload.RequestID,
				})
				n.sendError(client, "approval_forbidden", "approval belongs to a different session")
				return
			}
			logger.WarnCF("native", "Failed to handle approval", map[string]interface{}{
				"error":      err.Error(),
				"request_id": payload.RequestID,
			})
			n.sendError(client, "approval_error", "approval request expired or not found")
			return
		}
		command = handledApproval.Command

		n.persistApprovalMessage(client.SessionKey, payload.RequestID, payload.Approved, command, "")
	}

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

	if !n.validateSessionOwnership(client.ClientInfo.ClientID, sessionKey) {
		n.sendError(client, "forbidden", "access denied to this session")
		logger.WarnCF("native", "Subscribe ownership validation failed", map[string]interface{}{
			"client_id":   client.ID,
			"session_key": payload.SessionKey,
		})
		return
	}

	client.mu.Lock()
	client.SessionKey = sessionKey
	if client.Subscriptions == nil {
		client.Subscriptions = make(map[string]bool)
	}
	client.Subscriptions[sessionKey] = true
	client.mu.Unlock()

	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	processing := false
	if n.agentLoop != nil {
		processing = n.agentLoop.IsSessionProcessing(sessionKey)
	}

	ackData := map[string]interface{}{
		"session_key": sessionKey,
		"processing":  processing,
	}

	if processing {
		catchup := n.collectCatchupMessages(sessionKey, processing)
		if len(catchup) > 0 {
			ackData["in_progress_messages"] = catchup
		}
		n.addInProgressTool(ackData, sessionKey, processing)
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

	client.mu.Lock()
	oldSessionKey := client.SessionKey

	if payload.SessionKey != "" {
		delete(client.Subscriptions, payload.SessionKey)
	}

	if payload.SessionKey == "" || payload.SessionKey == client.SessionKey {
		client.SessionKey = client.ClientInfo.ClientID
	}
	nSubs := len(client.Subscriptions)
	newSessionKey := client.SessionKey
	client.mu.Unlock()

	logger.InfoCF("native", "Client unsubscribed from session", map[string]interface{}{
		"client_id":       client.ID,
		"old_session_key": oldSessionKey,
		"payload_key":     payload.SessionKey,
		"new_session_key": newSessionKey,
		"subscriptions":   nSubs,
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

	if !n.validateSessionOwnership(client.ClientInfo.ClientID, sessionKey) {
		n.sendError(client, "forbidden", "access denied to this session")
		return
	}

	n.emitNativeEvent(sessionKey, "typing.indicator", map[string]interface{}{
		"session_key": sessionKey,
		"client_id":   client.ClientInfo.ClientID,
		"device_name": client.ClientInfo.DeviceName,
	}, "")
}

func (n *NativeChannel) handleWSCancel(client *WSClient, data json.RawMessage, eventID string) {
	scope := CancelScopeSession
	if len(data) > 0 {
		var payload WSCancelPayload
		if err := json.Unmarshal(data, &payload); err != nil {
			n.sendError(client, "payload_error", "invalid cancel payload")
			return
		}
		if payload.Scope != "" {
			scope = payload.Scope
		}
	}
	if scope != CancelScopeSession && scope != CancelScopeTurn {
		n.sendError(client, "payload_error", "invalid cancel scope: "+scope)
		return
	}

	logger.InfoCF("native", "Cancel request received", map[string]interface{}{
		"client_id":   client.ID,
		"session_key": client.SessionKey,
		"scope":       scope,
	})

	var result string
	if scope == CancelScopeTurn {
		result = n.agentLoop.InterruptAgent(client.SessionKey)
	} else {
		result = n.agentLoop.StopAgent(client.SessionKey)
	}

	logger.InfoCF("native", "Cancel request processed", map[string]interface{}{
		"client_id":   client.ID,
		"session_key": client.SessionKey,
		"scope":       scope,
		"result":      result,
	})

	n.emitNativeEvent(client.SessionKey, "cancel.ack", map[string]interface{}{
		"status":      "cancelled",
		"session_key": client.SessionKey,
		"scope":       scope,
	}, "")
}

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

func (n *NativeChannel) sessionGroupSnapshots(sessionKey string) []group.GroupSnapshot {
	if n.agentLoop == nil {
		return nil
	}
	return n.agentLoop.GroupSnapshotsForSession(sessionKey)
}

func (n *NativeChannel) inProgressToolPayload(sessionKey string, processing bool) *WSToolExecutingPayload {
	if !processing || n.agentLoop == nil {
		return nil
	}
	inFlight := n.agentLoop.GetInProgressTool(sessionKey)
	if inFlight == nil || inFlight.Tool == "" {
		return nil
	}
	payload := &WSToolExecutingPayload{
		SessionKey:         sessionKey,
		Tool:               inFlight.Tool,
		Action:             inFlight.Action,
		ToolCallID:         inFlight.ToolCallID,
		SubagentSessionKey: inFlight.SubagentSessionKey,
	}
	if inFlight.Arguments != "" {
		var toolArgs map[string]interface{}
		if err := json.Unmarshal([]byte(inFlight.Arguments), &toolArgs); err == nil {
			payload.Arguments = toolArgs
		}
	}
	return payload
}

func (n *NativeChannel) addInProgressTool(data map[string]interface{}, sessionKey string, processing bool) {
	if payload := n.inProgressToolPayload(sessionKey, processing); payload != nil {
		data["in_progress_tool"] = payload
	}
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

	groupsEnabled := n.cfgSnapshot().GroupsFeatureEnabled()

	welcomeData := map[string]interface{}{
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
	}
	n.addInProgressTool(welcomeData, client.SessionKey, processing)

	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "welcome",
		Data:    mustMarshal(welcomeData),
	})); err != nil {
		logger.WarnCF("native", "Failed to send welcome", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
	}
}

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

	groupsEnabled := n.cfgSnapshot().GroupsFeatureEnabled()

	client.mu.Lock()
	subsSnapshot := client.Subscriptions
	client.mu.Unlock()

	reconnectedData := map[string]interface{}{
		"client_id":            client.ClientInfo.ClientID,
		"device_name":          client.ClientInfo.DeviceName,
		"session_key":          client.SessionKey,
		"status":               status,
		"agents":               agents,
		"server_time":          time.Now().Format(time.RFC3339),
		"processing":           processing,
		"buffered_events":      len(buffered),
		"disconnected_secs":    disconnectedSecs,
		"subscriptions":        subsSnapshot,
		"in_progress_messages": catchupMessages,
		"groups":               groups,
		"groups_enabled":       groupsEnabled,
	}
	n.addInProgressTool(reconnectedData, client.SessionKey, processing)

	if err := client.Send(mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "reconnected",
		Data:    mustMarshal(reconnectedData),
	})); err != nil {
		logger.WarnCF("native", "Failed to send reconnected event", map[string]interface{}{
			"client_id": client.ID,
			"error":     err.Error(),
		})
		return
	}

	flushed := 0
	skipped := 0
	for _, payload := range buffered {
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

	n.kickOutboundFlush(client)
}

func (n *NativeChannel) kickOutboundFlush(client *WSClient) {
	f := n.outboundFlusherSnapshot()
	if f == nil {
		return
	}
	f.FlushPeer(ChannelName, client.SessionKey)
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
	n.emitNativeEvent(sessionKey, "message.stream",
		newWSStreamPayload(messageID, sessionKey, chunk, done, nil), messageID)
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

func isValidSessionKeyFormat(sessionKey string) bool {
	if strings.TrimSpace(sessionKey) == "" {
		return false
	}
	if len(sessionKey) > 256 {
		return false
	}
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
	return true
}

func sessionKeyMatches(a, b string) bool {
	if a == b {
		return true
	}
	aNorm := strings.TrimPrefix(a, "native:")
	bNorm := strings.TrimPrefix(b, "native:")
	return aNorm == bNorm
}
