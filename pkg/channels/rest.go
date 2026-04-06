package channels

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func (n *NativeChannel) handleGetPIN(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	deviceName := getQueryParam(r, "device_name")

	pending, err := n.auth.GeneratePIN(deviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pin_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthPINResponse{
		PIN:     pending.PIN,
		Expires: pending.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	var req AuthPairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.PairWithPIN(req.PIN, req.DeviceName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "pair_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthPairResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
		ClientID:     client.ClientID,
	})
}

func (n *NativeChannel) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	var req AuthRefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	client, token, refreshToken, err := n.auth.RefreshToken(req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "refresh_error")
		return
	}

	writeJSON(w, http.StatusOK, AuthRefreshResponse{
		Token:        token,
		RefreshToken: refreshToken,
		Expires:      client.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	client, valid := n.auth.ValidateToken(token)

	writeJSON(w, http.StatusOK, AuthStatusResponse{
		Valid:      valid,
		ClientID:   client.ClientID,
		DeviceName: client.DeviceName,
		Expires:    client.Expires.Format(time.RFC3339),
	})
}

func (n *NativeChannel) handleChatSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	var req ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required", "content_missing")
		return
	}

	clientID := getClientID(r)
	sessionKey := req.SessionKey
	if sessionKey == "" {
		sessionKey = "native:" + clientID
	}

	if req.AgentID != "" {
		n.agentLoop.SetSessionAgent(sessionKey, req.AgentID)
	}

	messageID := uuid.New().String()

	attachments := make([]bus.FileAttachment, 0, len(req.Attachments))
	for _, path := range req.Attachments {
		attachments = append(attachments, bus.FileAttachment{
			Path: path,
			Name: path,
			Kind: "file",
		})
	}

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    clientID,
		ChatID:      sessionKey,
		Content:     req.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
	})

	writeJSON(w, http.StatusOK, ChatSendResponse{
		MessageID:  messageID,
		SessionKey: sessionKey,
	})
}

func (n *NativeChannel) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	sessionKey := getQueryParam(r, "session_key")
	if sessionKey == "" {
		clientID := getClientID(r)
		sessionKey = "native:" + clientID
	}

	messages := make([]map[string]interface{}, 0)

	writeJSON(w, http.StatusOK, ChatHistoryResponse{
		SessionKey: sessionKey,
		Messages:   messages,
	})
}

func (n *NativeChannel) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	clients := n.auth.ListClients()
	sessions := make([]ChatSession, 0, len(clients))
	for _, client := range clients {
		for _, sk := range client.SessionKeys {
			sessions = append(sessions, ChatSession{
				Key:          sk,
				Created:      client.Created,
				Updated:      client.LastSeen,
				MessageCount: 0,
			})
		}
	}

	writeJSON(w, http.StatusOK, ChatSessionsResponse{
		Sessions: sessions,
	})
}

func (n *NativeChannel) handleChatSession(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/api/v1/chat/session/"
	if !strings.HasPrefix(path, prefix) {
		writeError(w, http.StatusBadRequest, "invalid path", "path_invalid")
		return
	}

	sessionKey := strings.TrimPrefix(path, prefix)
	action := getQueryParam(r, "action")

	if r.Method == http.MethodDelete {
		n.agentLoop.ClearSession(sessionKey)
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
		return
	}

	if action == "summary" {
		writeJSON(w, http.StatusOK, map[string]string{"summary": ""})
		return
	}

	if action == "compact" {
		result := n.agentLoop.CompactSession(sessionKey)
		writeJSON(w, http.StatusOK, map[string]string{"result": result})
		return
	}

	writeError(w, http.StatusBadRequest, "unknown action", "action_invalid")
}

func (n *NativeChannel) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	agentIDs := n.agentLoop.ListAvailableAgentIDs()
	agents := make([]NativeAgentInfo, 0, len(agentIDs))
	defaultID := n.agentLoop.GetDefaultAgentID()

	for _, id := range agentIDs {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, NativeAgentInfo{
				ID:        info.ID,
				Name:      info.Name,
				Workspace: info.Workspace,
				Model:     info.Model,
				Default:   info.ID == defaultID,
			})
		}
	}

	writeJSON(w, http.StatusOK, AgentsResponse{Agents: agents})
}

func (n *NativeChannel) handleAgentInfo(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/api/v1/agents/"
	if !strings.HasPrefix(path, prefix) {
		writeError(w, http.StatusBadRequest, "invalid path", "path_invalid")
		return
	}

	agentID := strings.TrimPrefix(path, prefix)
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	action := getQueryParam(r, "action")
	if action == "status" {
		status := n.agentLoop.GetStatus("native:" + getClientID(r))
		writeJSON(w, http.StatusOK, AgentStatusResponse{
			ID:             agentID,
			Status:         status,
			ActiveSessions: 0,
		})
		return
	}

	writeJSON(w, http.StatusOK, NativeAgentInfo{
		ID:        info.ID,
		Name:      info.Name,
		Workspace: info.Workspace,
		Model:     info.Model,
		Default:   info.ID == n.agentLoop.GetDefaultAgentID(),
	})
}

func (n *NativeChannel) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfgMap := map[string]interface{}{
			"agents": map[string]interface{}{
				"defaults": map[string]interface{}{
					"workspace": n.cfgSnapshot().Agents.Defaults.Workspace,
					"provider":  n.cfgSnapshot().Agents.Defaults.Provider,
					"model":     n.cfgSnapshot().Agents.Defaults.Model,
				},
			},
			"channels": map[string]interface{}{
				"native": map[string]interface{}{
					"enabled": n.cfg.Enabled,
					"host":    n.cfg.Host,
					"port":    n.cfg.Port,
				},
			},
		}
		writeJSON(w, http.StatusOK, ConfigResponse{Config: cfgMap})

	case http.MethodPatch:
		var req ConfigUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "path": req.Path})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
	}
}

func (n *NativeChannel) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	tools := []ToolInfo{
		{Name: "read_file", Description: "Read file from workspace", Enabled: true},
		{Name: "write_file", Description: "Write file to workspace", Enabled: true},
		{Name: "list_dir", Description: "List directory contents", Enabled: true},
		{Name: "exec", Description: "Execute shell commands", Enabled: true},
		{Name: "web_search", Description: "Search the web", Enabled: true},
		{Name: "web_fetch", Description: "Fetch web content", Enabled: true},
		{Name: "spawn", Description: "Create subagent", Enabled: true},
	}

	writeJSON(w, http.StatusOK, ToolsResponse{Tools: tools})
}

func (n *NativeChannel) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	skills := []SkillInfo{}

	writeJSON(w, http.StatusOK, SkillsResponse{Skills: skills})
}

func (n *NativeChannel) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	uptime := time.Since(n.startTime).String()

	agents := make([]map[string]interface{}, 0)
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, map[string]interface{}{
				"id":     info.ID,
				"name":   info.Name,
				"status": "running",
			})
		}
	}

	channels := make([]map[string]interface{}, 0)
	channels = append(channels, map[string]interface{}{
		"name":    "native",
		"enabled": true,
		"running": n.running,
	})

	writeJSON(w, http.StatusOK, SystemStatusResponse{
		Status:   "running",
		Uptime:   uptime,
		Agents:   agents,
		Channels: channels,
		Version:  "1.0.0",
	})
}

func (n *NativeChannel) handleChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	channels := []ChannelInfo{
		{Name: "native", Enabled: true, Running: n.running},
	}

	writeJSON(w, http.StatusOK, ChannelsResponse{Channels: channels})
}

func (n *NativeChannel) cfgSnapshot() *config.Config {
	return nil
}
