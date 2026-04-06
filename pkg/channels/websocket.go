package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.ErrorCF("native", "WebSocket upgrade failed", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	clientID := uuid.New().String()
	sessionKey := getQueryParam(r, "session_key")
	if sessionKey == "" {
		sessionKey = "native:" + clientInfo.ClientID
	}

	client := &WSClient{
		ID:         clientID,
		Conn:       conn,
		ClientInfo: clientInfo,
		SessionKey: sessionKey,
		SendChan:   make(chan []byte, 100),
	}

	n.auth.TrackSessionKey(clientInfo.ClientID, sessionKey)
	n.addWSClient(client)
	n.auth.UpdateLastSeen(clientInfo.ClientID)

	logger.InfoCF("native", "WebSocket client connected", map[string]interface{}{
		"client_id":   clientID,
		"device_name": clientInfo.DeviceName,
		"session_key": sessionKey,
	})

	go n.wsReadLoop(client)
	go n.wsWriteLoop(client)
	go n.wsPingLoop(client)

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
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})
	conn.SetPingHandler(func(appData string) error {
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
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

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

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
	for {
		select {
		case data, ok := <-client.SendChan:
			if !ok {
				client.mu.Lock()
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				client.mu.Unlock()
				return
			}
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			client.mu.Lock()
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				client.mu.Unlock()
				logger.ErrorCF("native", "WebSocket write error", map[string]interface{}{
					"error": err.Error(),
				})
				return
			}
			client.mu.Unlock()
		}
	}
}

func (n *NativeChannel) wsPingLoop(client *WSClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	conn := client.Conn
	for {
		select {
		case <-ticker.C:
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
	switch msg.Event {
	case "message":
		n.handleWSClientMessage(client, msg.Data)

	case "approve":
		n.handleWSApprove(client, msg.Data)

	case "subscribe":
		n.handleWSSubscribe(client, msg.Data)

	case "unsubscribe":
		n.handleWSUnsubscribe(client, msg.Data)

	case "typing":
		n.handleWSTyping(client, msg.Data)

	case "cancel":
		n.handleWSCancel(client, msg.Data)

	case "ping":
		client.Send(mustMarshal(WSMessage{Event: "pong", Data: mustMarshal(map[string]string{"time": time.Now().Format(time.RFC3339)})}))

	default:
		n.sendError(client, "unknown_event", "unknown event type: "+msg.Event)
	}
}

func (n *NativeChannel) handleWSClientMessage(client *WSClient, data json.RawMessage) {
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
	n.auth.TrackSessionKey(client.ClientInfo.ClientID, sessionKey)

	if payload.AgentID != "" {
		n.agentLoop.SetSessionAgent(sessionKey, payload.AgentID)
	}

	messageID := uuid.New().String()

	attachments := make([]bus.FileAttachment, 0, len(payload.Attachments))
	for _, path := range payload.Attachments {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			logger.WarnCF("native", "Attachment file not found, skipping",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
				})
			continue
		}
		if err != nil {
			logger.WarnCF("native", "Failed to stat attachment, skipping",
				map[string]interface{}{
					"session_key": sessionKey,
					"path":        path,
					"error":       err.Error(),
				})
			continue
		}

		uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
		isTemporary := strings.HasPrefix(path, uploadDir)

		mimeType := detectMimeType(path)

		attachments = append(attachments, bus.FileAttachment{
			Path:      path,
			Name:      filepath.Base(path),
			MIMEType:  mimeType,
			Kind:      "file",
			Temporary: isTemporary,
		})

		logger.DebugCF("native", "Processed attachment",
			map[string]interface{}{
				"session_key": sessionKey,
				"path":        path,
				"temporary":   isTemporary,
			})
	}

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    client.ClientInfo.ClientID,
		ChatID:      sessionKey,
		Content:     payload.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
	})

	client.Send(mustMarshal(WSMessage{
		Event: "message.ack",
		Data:  mustMarshal(map[string]string{"message_id": messageID, "session_key": sessionKey}),
	}))
}

func (n *NativeChannel) handleWSApprove(client *WSClient, data json.RawMessage) {
	var payload WSApprovePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid approve payload")
		return
	}

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:    ChannelName,
		SenderID:   client.ClientInfo.ClientID,
		ChatID:     client.SessionKey,
		Content:    getApprovalCommand(payload.Approved, payload.RequestID),
		SessionKey: client.SessionKey,
	})

	client.Send(mustMarshal(WSMessage{
		Event: "approve.ack",
		Data:  mustMarshal(map[string]string{"request_id": payload.RequestID, "approved": boolToString(payload.Approved)}),
	}))
}

func (n *NativeChannel) handleWSSubscribe(client *WSClient, data json.RawMessage) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid subscribe payload")
		return
	}

	client.SessionKey = payload.SessionKey
	n.auth.TrackSessionKey(client.ClientInfo.ClientID, payload.SessionKey)

	client.Send(mustMarshal(WSMessage{
		Event: "subscribe.ack",
		Data:  mustMarshal(map[string]string{"session_key": payload.SessionKey}),
	}))
}

func (n *NativeChannel) handleWSUnsubscribe(client *WSClient, data json.RawMessage) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		n.sendError(client, "payload_error", "invalid unsubscribe payload")
		return
	}

	client.SessionKey = "native:" + client.ClientInfo.ClientID

	client.Send(mustMarshal(WSMessage{
		Event: "unsubscribe.ack",
		Data:  mustMarshal(map[string]string{"session_key": payload.SessionKey}),
	}))
}

func (n *NativeChannel) handleWSTyping(client *WSClient, data json.RawMessage) {
	var payload WSSubscribePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
}

func (n *NativeChannel) handleWSCancel(client *WSClient, data json.RawMessage) {
	n.agentLoop.StopAgent(client.SessionKey)

	client.Send(mustMarshal(WSMessage{
		Event: "cancel.ack",
		Data:  mustMarshal(map[string]string{"status": "cancelled"}),
	}))
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

	client.Send(mustMarshal(WSMessage{
		Event: "welcome",
		Data: mustMarshal(map[string]interface{}{
			"client_id":   client.ClientInfo.ClientID,
			"device_name": client.ClientInfo.DeviceName,
			"session_key": client.SessionKey,
			"status":      status,
			"agents":      agents,
			"server_time": time.Now().Format(time.RFC3339),
		}),
	}))
}

func (n *NativeChannel) sendError(client *WSClient, code, message string) {
	client.Send(mustMarshal(WSMessage{
		Event: "error",
		Data:  mustMarshal(WSErrorPayload{Code: code, Message: message}),
	}))
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

func getApprovalCommand(approved bool, requestID string) string {
	if approved {
		return "/approve " + requestID
	}
	return "/reject " + requestID
}

func (n *NativeChannel) RegisterOutboundHandler(ctx context.Context) {
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

			messageID := msg.MessageID
			if messageID == "" {
				messageID = uuid.New().String()
			}

			if len(msg.Content) > 0 {
				n.StreamMessage(msg.ChatID, messageID, msg.Content, true)
			}

			if len(msg.Attachments) > 0 {
				n.broadcastToSession(msg.ChatID, "attachments", attachmentsToMaps(msg.Attachments))
			}
		}
	}
}
