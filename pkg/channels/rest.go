package channels

import (
	"encoding/json"
	"net/http"
	"sort"
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
	if token == authHeader {
		writeJSON(w, http.StatusOK, AuthStatusResponse{Valid: false})
		return
	}

	client, valid := n.auth.ValidateToken(token)
	resp := AuthStatusResponse{Valid: valid}
	if valid && client != nil {
		resp.ClientID = client.ClientID
		resp.DeviceName = client.DeviceName
		resp.Expires = client.Expires.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
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
	n.auth.TrackSessionKey(clientID, sessionKey)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if req.AgentID != "" {
		n.agentLoop.SetSessionAgent(sessionKey, req.AgentID)
	}

	messageID := uuid.New().String()

	attachments := n.processAttachments(req.Attachments, sessionKey)

	n.bus.PublishInbound(bus.InboundMessage{
		Channel:     ChannelName,
		SenderID:    clientID,
		ChatID:      sessionKey,
		Content:     req.Content,
		Attachments: attachments,
		SessionKey:  sessionKey,
		Metadata:    map[string]string{"message_id": messageID},
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

	clientID := getClientID(r)
	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	history := n.agentLoop.GetSessionHistory(sessionKey)
	messages := make([]ChatHistoryMessage, 0, len(history))
	for _, msg := range history {
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "tool" {
			continue
		}

		historyMsg := ChatHistoryMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		if len(msg.ToolCalls) > 0 {
			historyMsg.ToolCalls = make([]HistoryToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				historyMsg.ToolCalls = append(historyMsg.ToolCalls, HistoryToolCall{
					ID:               tc.ID,
					Type:             tc.Type,
					Name:             tc.Name,
					Arguments:        tc.Arguments,
					ThoughtSignature: tc.ThoughtSignature,
				})
			}
		}
		messages = append(messages, historyMsg)
	}

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

	clientID := getClientID(r)
	client, ok := n.auth.GetClient(clientID)
	if !ok {
		writeJSON(w, http.StatusOK, ChatSessionsResponse{Sessions: []ChatSession{}})
		return
	}

	sessions := make([]ChatSession, 0, len(client.SessionKeys))
	for _, sk := range client.SessionKeys {
		history := n.agentLoop.GetSessionHistory(sk)
		messageCount := 0
		for _, msg := range history {
			if msg.Role == "user" || msg.Role == "assistant" {
				messageCount++
			}
		}

		sessions = append(sessions, ChatSession{
			Key:          sk,
			Name:         n.agentLoop.GetName(sk),
			Created:      client.Created,
			Updated:      n.agentLoop.GetUpdated(sk),
			MessageCount: messageCount,
		})
	}

	writeJSON(w, http.StatusOK, ChatSessionsResponse{
		Sessions: sessions,
	})
}

func (n *NativeChannel) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	if req.SessionKey == "" {
		writeError(w, http.StatusBadRequest, "session_key is required", "session_key_missing")
		return
	}

	clientID := getClientID(r)
	expectedPrefix := "native:" + clientID
	if req.SessionKey != expectedPrefix && !strings.HasPrefix(req.SessionKey, expectedPrefix+":") {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	n.auth.TrackSessionKey(clientID, req.SessionKey)

	writeJSON(w, http.StatusOK, CreateSessionResponse{
		SessionKey: req.SessionKey,
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
	clientID := getClientID(r)
	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}
	action := getQueryParam(r, "action")

	if r.Method == http.MethodDelete {
		action := getQueryParam(r, "action")
		if action == "delete" {
			if err := n.auth.RemoveSessionKey(clientID, sessionKey); err != nil {
				writeError(w, http.StatusBadRequest, err.Error(), "session_not_found")
				return
			}
			n.agentLoop.ClearSession(sessionKey)
			writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
			return
		}
		n.agentLoop.ClearSession(sessionKey)
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
		return
	}

	if action == "summary" {
		writeJSON(w, http.StatusOK, map[string]string{"summary": ""})
		return
	}

	if action == "model" {
		switch r.Method {
		case http.MethodGet:
			agentID := n.agentLoop.GetSessionAgent(sessionKey)
			models := n.listAllModels()
			writeJSON(w, http.StatusOK, SessionModelResponse{
				SessionKey:  sessionKey,
				AgentID:     agentID,
				Model:       n.agentLoop.GetSessionModel(sessionKey),
				Models:      models,
				ModelGroups: n.buildModelGroups(agentID, models),
			})
			return
		case http.MethodPatch:
			var req SessionModelUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
				return
			}
			if strings.TrimSpace(req.Model) == "" {
				writeError(w, http.StatusBadRequest, "model is required", "model_missing")
				return
			}
			agentID := n.agentLoop.GetSessionAgent(sessionKey)
			models := n.listAllModels()
			writeJSON(w, http.StatusOK, SessionModelResponse{
				SessionKey:  sessionKey,
				AgentID:     agentID,
				Model:       n.agentLoop.SetSessionModel(sessionKey, req.Model),
				Models:      models,
				ModelGroups: n.buildModelGroups(agentID, models),
			})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
			return
		}
	}

	if action == "agent" {
		switch r.Method {
		case http.MethodGet:
			agentID := n.agentLoop.GetSessionAgent(sessionKey)
			writeJSON(w, http.StatusOK, SessionAgentResponse{
				SessionKey: sessionKey,
				AgentID:    agentID,
			})
			return
		case http.MethodPatch:
			var req SessionAgentUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
				return
			}
			if strings.TrimSpace(req.AgentID) == "" {
				writeError(w, http.StatusBadRequest, "agent_id is required", "agent_id_missing")
				return
			}
			n.agentLoop.SetSessionAgent(sessionKey, req.AgentID)
			writeJSON(w, http.StatusOK, SessionAgentResponse{
				SessionKey: sessionKey,
				AgentID:    n.agentLoop.GetSessionAgent(sessionKey),
			})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
			return
		}
	}

	if action == "compact" {
		result := n.agentLoop.CompactSession(sessionKey)
		writeJSON(w, http.StatusOK, map[string]string{"result": result})
		return
	}

	if action == "name" {
		switch r.Method {
		case http.MethodGet:
			name := n.agentLoop.GetName(sessionKey)
			writeJSON(w, http.StatusOK, SessionNameResponse{
				SessionKey: sessionKey,
				Name:       name,
			})
			return
		case http.MethodPatch:
			var req SessionNameUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				writeError(w, http.StatusBadRequest, "name is required", "name_missing")
				return
			}
			if err := n.agentLoop.SetName(sessionKey, req.Name); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error(), "name_update_failed")
				return
			}
			writeJSON(w, http.StatusOK, SessionNameResponse{
				SessionKey: sessionKey,
				Name:       n.agentLoop.GetName(sessionKey),
			})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
			return
		}
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
	configPath := n.getConfigPath()

	switch r.Method {
	case http.MethodGet:
		doc, meta, err := config.LoadEditableDocument(configPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load config: "+err.Error(), "config_load_failed")
			return
		}

		response := ConfigResponse{
			Config: doc,
			Metadata: ConfigMetadata{
				ConfigPath:              meta.ConfigPath,
				Source:                  meta.Source,
				CanSave:                 meta.CanSave,
				RestartRequiredSections: meta.RestartRequiredSections,
				SecretsByPath:           meta.SecretsByPath,
			},
		}
		writeJSON(w, http.StatusOK, response)

	case http.MethodPut:
		var req ConfigUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
			return
		}
		body, err := json.Marshal(req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
			return
		}
		var doc config.EditableDocument
		if err := json.Unmarshal(body, &doc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
			return
		}

		// Validar documento
		validationErrors := config.ValidateEditableDocument(&doc)
		if len(validationErrors) > 0 {
			httpErrors := make([]ConfigError, len(validationErrors))
			for i, err := range validationErrors {
				httpErrors[i] = ConfigError{
					Path:    err.Path,
					Message: err.Message,
					Code:    err.Code,
				}
			}
			writeJSON(w, http.StatusBadRequest, ConfigUpdateResponse{
				Errors: httpErrors,
			})
			return
		}

		// Convertir a Config para validación adicional
		_, err = doc.ToConfig()
		if err != nil {
			writeError(w, http.StatusBadRequest, "config validation failed: "+err.Error(), "config_invalid")
			return
		}

		// Guardar documento
		if err := config.SaveEditableDocument(configPath, &doc); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save config: "+err.Error(), "config_save_failed")
			return
		}

		// Recargar metadata después de guardar
		_, meta, err := config.LoadEditableDocument(configPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload config: "+err.Error(), "config_reload_failed")
			return
		}

		response := ConfigUpdateResponse{
			Config: &doc,
			Metadata: ConfigMetadata{
				ConfigPath:              meta.ConfigPath,
				Source:                  meta.Source,
				CanSave:                 meta.CanSave,
				RestartRequiredSections: meta.RestartRequiredSections,
				SecretsByPath:           meta.SecretsByPath,
			},
		}
		writeJSON(w, http.StatusOK, response)

	case http.MethodPost:
		if r.URL.Path != "/api/v1/config/validate" {
			writeError(w, http.StatusNotFound, "not found", "not_found")
			return
		}

		var req ConfigValidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "body_invalid")
			return
		}
		body, err := json.Marshal(req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config payload", "body_invalid")
			return
		}
		var doc config.EditableDocument
		if err := json.Unmarshal(body, &doc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid config payload: "+err.Error(), "body_invalid")
			return
		}

		validationErrors := config.ValidateEditableDocument(&doc)
		if _, err := doc.ToConfig(); err != nil {
			validationErrors = append(validationErrors, config.ValidationError{
				Path:    "config",
				Message: err.Error(),
				Code:    "config_invalid",
			})
		}

		if len(validationErrors) > 0 {
			httpErrors := make([]ConfigError, len(validationErrors))
			for i, err := range validationErrors {
				httpErrors[i] = ConfigError{
					Path:    err.Path,
					Message: err.Message,
					Code:    err.Code,
				}
			}
			writeJSON(w, http.StatusOK, ConfigValidateResponse{
				Valid:  false,
				Errors: httpErrors,
			})
			return
		}

		writeJSON(w, http.StatusOK, ConfigValidateResponse{
			Valid:  true,
			Errors: nil,
		})

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

func (n *NativeChannel) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	agentID := getQueryParam(r, "agent_id")
	if agentID == "" {
		agentID = n.agentLoop.GetSessionAgent("native:" + getClientID(r))
	}

	models := n.listAllModels()
	modelGroups := n.buildModelGroups(agentID, models)
	model := ""
	if sessionKey := getQueryParam(r, "session_key"); sessionKey != "" {
		model = n.agentLoop.GetSessionModel(sessionKey)
	}

	writeJSON(w, http.StatusOK, ModelsResponse{
		AgentID:     agentID,
		Model:       model,
		Models:      models,
		ModelGroups: modelGroups,
	})
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

func (n *NativeChannel) buildModelGroups(_ string, _ []string) []ModelGroup {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	groups := make([]ModelGroup, 0, len(providerNames))
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		if len(aliases) == 0 {
			continue
		}

		group := ModelGroup{
			Provider: providerName,
			Models:   make([]ModelOption, 0, len(aliases)),
		}
		for _, alias := range aliases {
			resolved := strings.TrimSpace(provider.Models[alias].Model)
			value := alias
			if resolved != "" {
				value = providerName + "/" + resolved
			} else {
				value = providerName + "/" + alias
			}
			group.Models = append(group.Models, ModelOption{Value: value, Label: alias})
		}
		groups = append(groups, group)
	}

	if len(groups) == 0 {
		return nil
	}
	return groups
}

func (n *NativeChannel) listAllModels() []string {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			key := providerName + "/" + alias
			if seen[key] {
				continue
			}
			models = append(models, key)
			seen[key] = true
		}
	}

	return models
}

func (n *NativeChannel) cfgSnapshot() *config.Config {
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			return cfg
		}
	}

	cfg := config.DefaultConfig()
	if n.cfg != nil {
		cfg.Channels.Native = *n.cfg
	}
	return cfg
}
